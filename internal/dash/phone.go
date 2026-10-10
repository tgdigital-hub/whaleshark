package dash

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// A request to pair is gone after pairLife; a session in use is marked
	// as seen, and its cookie given a new end, once every seenEvery.
	pairLife  = contract.PairLife
	seenEvery = 5 * time.Minute
	// bound begins the name of every cookie set at the door: a browser takes
	// such a one only over HTTPS and only from the very name it is for, so
	// that no other name of the same domain can set or replace it.
	bound = "__Host-"
)

// pairing is the one request to pair that can be open: the number the phone
// shows, the hash of the secret in its cookie, who the road said was
// calling, and the kind of phone. at is zero once the person refused it.
type pairing struct {
	number, hash, who, name string
	notes                   []string
	at                      time.Time
}

func (p pairing) id() string { return "pair" + strings.ReplaceAll(p.number, " ", "") }

type viaPhone struct{}

// from is the paired phone a request came in by; empty on the laptop's road.
func from(ctx context.Context) string {
	id, _ := ctx.Value(viaPhone{}).(string)
	return id
}

// doors opens the door for phones or closes it, as phones.json says now.
func (s *server) doors() {
	peek, port := s.k.Platform.Peek, 0
	srv, err := contract.ReadServer(peek)
	if phones, perr := contract.ReadPhones(peek, s.dirs.State); err == nil && perr == nil && phones.On {
		port = srv.Door(s.login)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.door != nil && port != s.port {
		s.door.Close()
		s.door, s.port = nil, 0
	}
	if s.door != nil || port == 0 {
		return
	}
	l, err := listen("tcp4", net.JoinHostPort(contract.Loopback, strconv.Itoa(port)), port)
	if err != nil {
		return
	}
	s.door, s.port = &http.Server{Handler: http.HandlerFunc(s.phone), ReadHeaderTimeout: 10 * time.Second}, port
	go s.door.Serve(l)
	if a, known := srv.Access(s.login); known {
		go s.find(a.Issuer(), "", contract.Now())
	}
}

// phone answers at the door. What comes in there was handed over by the
// road's own program on this machine, over HTTPS from the phone: on
// Tailscale's road its header says who is calling, on Cloudflare's the pass
// the door checks itself, and what fails that check is answered with
// nothing. A phone that is not paired is shown a number; a paired one gets
// the page, in one column.
func (s *server) phone(w http.ResponseWriter, r *http.Request) {
	head(w, r)
	now, peek, path := contract.Now(), s.k.Platform.Peek, r.URL.Path
	srv, err := contract.ReadServer(peek)
	phones, perr := contract.ReadPhones(peek, s.dirs.State)
	post := r.Method == http.MethodPost
	open := err == nil && perr == nil && srv != nil && phones.On
	var who string
	switch {
	case open && srv.Road == contract.RoadTailscale:
		who = contract.Identity(srv.Road, r.Header.Get)
	case open && srv.Road == contract.RoadCloudflare:
		var until time.Time
		if who, until, open = s.pass(r, srv, now); open {
			// A held-open connection ends when its pass runs out.
			ctx, cancel := context.WithTimeout(r.Context(), until.Sub(now))
			defer cancel()
			r = r.WithContext(ctx)
		}
	default:
		open = false
	}
	if !open {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if !s.asked(w, r, "https://", strings.HasPrefix(path, contract.RouteDo) || post && path == contract.RoutePush) {
		return
	}
	if name, asset := strings.CutPrefix(path, contract.RouteAsset); asset {
		s.asset(w, name)
		return
	}
	secret := ""
	if c, err := r.Cookie(bound + s.cookie); err == nil {
		secret = c.Value
	}
	s.mu.Lock()
	s.caller = who
	s.mu.Unlock()
	hash := contract.TokenHash(secret)
	at := slices.IndexFunc(phones.Paired, func(p contract.Phone) bool { return secret != "" && same(p.Session, hash) })
	if at < 0 || now.Sub(phones.Paired[at].SeenAt) >= sessionLife {
		s.knock(w, r, srv, who, hash, now)
		return
	}
	ph := phones.Paired[at]
	if ph.Identity != who {
		s.draw(w, http.StatusForbidden, &contract.Page{Screen: contract.ScreenRefused, Said: said("other_identity", "This phone was paired by somebody else.")})
		return
	}
	if now.Sub(ph.SeenAt) >= seenEvery && s.record(ph.ID, func(p *contract.Phone) { p.SeenAt = now }) == nil {
		s.bake(w, secret)
	}
	r = r.WithContext(context.WithValue(r.Context(), viaPhone{}, ph.ID))
	switch path {
	case contract.RoutePair:
		http.Redirect(w, r, contract.RouteTeam, http.StatusSeeOther)
	case contract.RoutePush:
		s.push(w, r, ph)
	default:
		s.route(w, r, ph.Session)
	}
}

// record changes one paired phone's entry.
func (s *server) record(id string, set func(*contract.Phone)) error {
	return contract.ChangePhones(s.k.Platform, func(list *contract.Phones) error {
		for i := range list.Paired {
			if list.Paired[i].ID == id {
				set(&list.Paired[i])
			}
		}
		return nil
	})
}

// bake sets a phone's cookie: the secret its session is found by. The door
// is HTTPS, so the cookie is one a browser sends over nothing else; scripts
// cannot read it and no other site's request carries it.
func (s *server) bake(w http.ResponseWriter, secret string) {
	http.SetCookie(w, &http.Cookie{Name: bound + s.cookie, Value: secret, Path: "/", MaxAge: int(sessionLife / time.Second),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

// knock answers a phone that is not paired: its number and nothing else,
// under one address. One request is open at a time; a second phone is
// refused until the first is decided or two minutes old, and after five
// refusals the next request waits, as a wrong sign-in code does.
func (s *server) knock(w http.ResponseWriter, r *http.Request, srv *contract.Server, who, hash string, now time.Time) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path != contract.RoutePair {
		http.Redirect(w, r, contract.RoutePair, http.StatusSeeOther)
		return
	}
	var no *contract.Refusal
	code, fresh := http.StatusTooManyRequests, false
	s.mu.Lock()
	p := s.pair
	open := p.hash != "" && now.Sub(p.at) < pairLife
	switch {
	case open && p.hash == hash:
	case open:
		no = said("pairing_busy", "Another phone is being paired. Try again in two minutes.")
	case p.hash == hash:
		s.pair, code = pairing{}, http.StatusForbidden
		no = said("pairing_over", "That number was refused or ran out. Load this page again for a new one.")
	case s.refusals >= slowFrom && now.Sub(s.refused) < time.Second<<min(s.refusals-slowFrom, slowMost):
		no = said("pairing_slow", "Too many phones were refused: wait a little.")
	default:
		var n [4]byte
		rand.Read(n[:])
		secret, six := rand.Text(), binary.BigEndian.Uint32(n[:])%1_000_000
		p = pairing{number: fmt.Sprintf("%03d %03d", six/1000, six%1000), hash: contract.TokenHash(secret), who: who, name: "a browser", at: now}
		for _, kind := range []string{"iPhone", "iPad", "Android"} {
			if strings.Contains(r.UserAgent(), kind) {
				p.name = kind
			}
		}
		if who == "" {
			p.notes = append(p.notes, "the private network names nobody for this phone: its session will stand alone")
		}
		if srv.Road == contract.RoadTailscale && srv.Rules != contract.RulesProven {
			p.notes = append(p.notes, "this server's port rules are unproven: pairing and the session are the only wall between its logins")
		}
		s.pair, fresh = p, true
		s.bake(w, secret)
	}
	s.mu.Unlock()
	if fresh {
		// Written down for the panes and the terminal, which see no memory
		// of this server's; what is decided is decided here all the same.
		s.asking(&contract.Pairing{Number: p.number, Identity: p.who, Name: p.name, Notes: p.notes, At: p.at})
		s.wake()
	}
	if no != nil {
		s.draw(w, code, &contract.Page{Screen: contract.ScreenRefused, Said: no})
		return
	}
	s.draw(w, http.StatusOK, &contract.Page{Screen: contract.ScreenPair, Phone: true, Pair: p.number, Notes: p.notes})
}

// decide is the person's answer to the phone that asks, given where they
// are signed in already. ok is false when id is not the open request's.
func (s *server) decide(id, answer string, now time.Time) (ok bool, err error) {
	s.mu.Lock()
	p := s.pair
	if p.hash == "" || p.id() != id || now.Sub(p.at) >= pairLife {
		s.mu.Unlock()
		return false, nil
	}
	if answer == "approve" {
		s.pair, s.refusals = pairing{}, 0
	} else {
		s.pair.at, s.refusals, s.refused = time.Time{}, s.refusals+1, now
	}
	s.mu.Unlock()
	defer s.wake()
	return true, contract.ChangePhones(s.k.Platform, func(list *contract.Phones) error {
		if list.Asking = nil; answer != "approve" {
			return nil
		}
		n := 1
		for slices.ContainsFunc(list.Paired, func(q contract.Phone) bool { return q.ID == "ph"+strconv.Itoa(n) }) {
			n++
		}
		list.Paired = append(list.Paired, contract.Phone{ID: "ph" + strconv.Itoa(n), Name: p.name, Identity: p.who, Session: p.hash, PairedAt: now, SeenAt: now})
		return nil
	})
}

// asking writes down the request to pair that the screens show; shutNow
// writes down that the door has just shut itself.
func (s *server) asking(p *contract.Pairing) {
	contract.ChangePhones(s.k.Platform, func(list *contract.Phones) error { list.Asking = p; return nil })
}

func (s *server) shutNow(now time.Time) {
	contract.ChangePhones(s.k.Platform, func(list *contract.Phones) error { list.Shut = now; return nil })
}

// withPhones lists each paired phone under the answered items of a screen of
// the laptop's road, with the button that forgets it, which is a child and
// so needs a run.
func withPhones(v *contract.View, list contract.Phones) {
	for _, ph := range list.Paired {
		is := contract.Item{Form: contract.FormChoice, Look: contract.LookNeedsYou, Source: "from the tool itself", ID: ph.ID, Since: ph.PairedAt}
		is.Text = "A paired phone: " + ph.Name + " · " + whose(ph.Identity) + " · last seen " + ph.SeenAt.In(v.At.Location()).Format("2 Jan 15:04")
		is.News = is.Text
		is.Buttons = []contract.Button{{Label: "Forget this phone", Action: "forget-phone"}}
		v.Answered = append(v.Answered, is)
	}
}

func whose(identity string) string {
	if identity == "" {
		return "nobody named"
	}
	return identity
}

// push answers a paired phone's script with the login's public key for
// nudges, and takes what the phone's browser then hands over, as given.
func (s *server) push(w http.ResponseWriter, r *http.Request, ph contract.Phone) {
	key := s.k.PushKey()
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, key)
		return
	}
	to := &contract.PushTo{Endpoint: r.PostFormValue("endpoint"), P256dh: r.PostFormValue("p256dh"), Auth: r.PostFormValue("auth")}
	switch {
	case !same(r.PostFormValue(contract.FieldToken), mac(s.key, "token", ph.Session)):
		w.WriteHeader(http.StatusForbidden)
	case key == "" || to.Endpoint == "" || to.P256dh == "" || to.Auth == "":
		w.WriteHeader(http.StatusBadRequest)
	case s.record(ph.ID, func(p *contract.Phone) { p.Push = to }) != nil:
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// listed is what `dash phone` answers: the door and the paired phones,
// without what a session or a nudge is sealed with.
type listed struct {
	On     bool     `json:"on"`
	Port   int      `json:"port,omitempty"`
	Name   string   `json:"name,omitempty"`
	Phones []paired `json:"phones"`
	// Asking is the phone that asks to be paired now, if one does.
	Asking *contract.Pairing `json:"asking,omitempty"`
}

type paired struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Identity string    `json:"identity,omitempty"`
	PairedAt time.Time `json:"paired_at"`
	SeenAt   time.Time `json:"seen_at"`
	Nudges   bool      `json:"nudges"`
}

// phones is `dash phone on|off|list|forget <phone>|approve <number>|refuse
// <number>`. The door needs a
// server whose administrator has set this login's port for a phone; who
// may come through it is decided at each pairing, never here.
func phones(c *contract.Call, dirs contract.Dirs, running bool) (any, error) {
	k, login := c.Kit, contract.Me()
	word, id, words := "", "", 2
	if len(c.Args) > 1 {
		word = c.Args[1]
	}
	decides := word == "approve" || word == "refuse"
	if word == "forget" || decides {
		words = 3
		id = c.Args[len(c.Args)-1]
	}
	srv, err := contract.ReadServer(k.Platform.Peek)
	a, public := srv.Access(login)
	out := listed{Phones: []paired{}}
	var line string
	switch {
	case len(c.Args) != words, word != "on" && word != "off" && word != "list" && word != "forget" && !decides:
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "dash phone takes on, off, list, forget <phone>, approve <number> or refuse <number>.", Next: []string{"whaleshark help dash"}}
	case decides:
		// The number as the phone shows it, or the item's id: the running
		// server decides, since only it holds what the phone was handed.
		number := strings.ReplaceAll(strings.TrimPrefix(id, "pair"), " ", "")
		if st, _ := ask(k, dirs, word, number); !st.Decided {
			return nil, &contract.Refusal{Exit: contract.ExitMissing, Code: "no_request", Message: "No phone is asking to be paired under the number " + number + ": it was decided already, or its two minutes ran out.", Next: []string{"whaleshark dash phone list"}}
		}
		fmt.Fprintln(c.Out, map[string]string{"approve": "The phone is paired: it shows the page now.", "refuse": "The phone was refused."}[word])
		return map[string]string{"number": number, "answer": word}, nil
	case word == "on" && (err != nil || srv == nil || srv.Login[login].Phone == 0 && !public):
		return nil, refuse("no_door", "This login has no door for a phone: the administrator of a server sets one.", "sudo whaleshark server phone --user "+login)
	}
	found := word != "forget"
	err = contract.ChangePhones(k.Platform, func(list *contract.Phones) error {
		if word == "on" || word == "off" {
			list.On = word == "on"
		}
		list.Paired = slices.DeleteFunc(list.Paired, func(p contract.Phone) bool {
			found = found || p.ID == id
			return p.ID == id
		})
		out.On, out.Asking = list.On, list.Asking
		if a := out.Asking; a != nil && c.Now.Sub(a.At) >= contract.PairLife {
			out.Asking = nil
		}
		for _, p := range list.Paired {
			out.Phones = append(out.Phones, paired{p.ID, p.Name, p.Identity, p.PairedAt, p.SeenAt, p.Push != nil})
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, &contract.Refusal{Exit: contract.ExitMissing, Code: "no_such_phone", Message: "No paired phone is called " + id + ".", Next: []string{"whaleshark dash phone list"}}
	case word == "on" && !running:
		_, err = start(k, dirs)
	}
	if word == "on" || word == "off" {
		if st, ok := ask(k, dirs, "door"); word == "on" && (err != nil || !ok || st.Door == 0) {
			return nil, refuse("door_shut", "Phone access is on, but the door did not open.", "whaleshark dash status")
		}
	}
	if out.On && srv != nil {
		out.Port, out.Name = srv.Login[login].Phone, a.Name
	}
	switch word {
	case "on":
		line = fmt.Sprintf("The door for your phone is open. On the phone, inside the private network, open\n  https://<this machine's name there>:%d/\nand approve its number on the page here: whaleshark dash url", out.Port)
		if public {
			line = "The door for your phone is open. In any browser, open\n  https://" + a.Name + "/\nlog in at Cloudflare, and approve its number on the page here: whaleshark dash url"
		}
	case "off":
		line = "The door for phones is closed. Paired phones stay paired."
	case "forget":
		line = "Phone " + id + " is forgotten: its session has ended."
	default:
		line = "The door for phones is " + map[bool]string{true: "open", false: "closed"}[out.On] + "."
		for _, p := range out.Phones {
			line += fmt.Sprintf("\n  %s  %s  %s  paired %s  last seen %s  nudges %s", p.ID, p.Name, whose(p.Identity),
				p.PairedAt.Local().Format("2 Jan 15:04"), p.SeenAt.Local().Format("2 Jan 15:04"), map[bool]string{true: "on", false: "off"}[p.Nudges])
		}
		if a := out.Asking; a != nil {
			number := strings.ReplaceAll(a.Number, " ", "")
			line += fmt.Sprintf("\nA phone asks to be paired: %s · %s · %s\n> whaleshark dash phone approve %s\n> whaleshark dash phone refuse %[4]s", a.Number, whose(a.Identity), a.Name, number)
		}
	}
	fmt.Fprintln(c.Out, line)
	return out, nil
}
