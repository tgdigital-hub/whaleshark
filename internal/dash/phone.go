package dash

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
	at                      time.Time
}

func (p pairing) id() string { return "pair" + strings.ReplaceAll(p.number, " ", "") }

type viaPhone struct{}

// from is the paired phone a request came in by; empty on the laptop's road.
func from(ctx context.Context) string {
	id, _ := ctx.Value(viaPhone{}).(string)
	return id
}

// plainRoad marks a request that came to the door over ssh's forward, as
// plain HTTP under a name of the machine itself.
type plainRoad struct{}

// jar is the name a door's cookie begins with and whether it is Secure: over
// HTTPS bound to the one name, over ssh's forward one of its own that a
// browser sends over plain HTTP to the machine's own name.
func jar(ctx context.Context) (prefix string, secure bool) {
	if ctx.Value(plainRoad{}) != nil {
		return "p", false
	}
	return bound, true
}

// door is the login's port for phones: on a server the last of its block,
// where no server is the one the person picked (phone.port); 0 for none.
func door(k *contract.Kit, dirs contract.Dirs, srv *contract.Server, login string) int {
	if cfg, _ := contract.ReadPerson(k.Platform.Peek, dirs.Config); srv == nil {
		return cfg.Phone.Port
	}
	return srv.Door(login)
}

// local says whether a request on the road where no server is asks under a
// name of the machine itself, as over ssh's forward (plain), or under the
// one the person gave the program in front of it, which ends HTTPS.
func (s *server) local(host string) (ok, plain bool) {
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	cfg, _ := contract.ReadPerson(s.k.Platform.Peek, s.dirs.Config)
	plain = host == "localhost" || host == contract.Loopback
	return plain || cfg.Phone.Name != "" && strings.EqualFold(host, cfg.Phone.Name), plain
}

// doors opens the door for phones or closes it, as phones.json says now.
func (s *server) doors() {
	peek, port := s.k.Platform.Peek, 0
	srv, err := contract.ReadServer(peek)
	if phones, perr := contract.ReadPhones(peek, s.dirs.State); err == nil && perr == nil && phones.On {
		port = door(s.k, s.dirs, srv, s.login)
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
	if s.closed = ""; err != nil {
		s.closed = err.Error()
		return
	}
	s.door, s.port = &http.Server{Handler: http.HandlerFunc(s.phone), ReadHeaderTimeout: 10 * time.Second}, port
	go s.door.Serve(l)
	if a, known := srv.Access(s.login); known {
		go s.find(a.Issuer(), "", contract.Now())
	}
}

// phone answers at the door. What comes in there was handed over by the
// road's own program on this machine, over HTTPS from the phone, or by the
// person's own ssh. The one place below asks who is calling, by this
// server's road, and a road it does not know names nobody: on Cloudflare's
// it is the pass, which the door checks itself, and what fails that check
// is answered with nothing; where no server is, nobody is named and only
// the host name is checked. A phone that is not paired is shown a number; a
// paired one gets the page, in one column.
func (s *server) phone(w http.ResponseWriter, r *http.Request) {
	head(w, r)
	now, peek, path := contract.Now(), s.k.Platform.Peek, r.URL.Path
	srv, err := contract.ReadServer(peek)
	phones, perr := contract.ReadPhones(peek, s.dirs.State)
	post := r.Method == http.MethodPost
	open, scheme := err == nil && perr == nil && phones.On, "https://"
	var who string
	switch plain := false; {
	case open && srv == nil:
		// Every login of the machine can reach its own port: whoever does,
		// and is not paired, is shown a number as on every road.
		if open, plain = s.local(r.Host); open && plain {
			scheme, r = "http://", r.WithContext(context.WithValue(r.Context(), plainRoad{}, true))
		}
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
	// An app names no origin: a request that names one is a browser's, and
	// only the page's own is let through to the interface.
	api := path == contract.RouteAPI || strings.HasPrefix(path, contract.APIv1+"/")
	if origin := r.Header.Get("Origin"); api && origin != "" && origin != scheme+r.Host {
		reply(w, http.StatusForbidden, said("wrong_origin", "That request did not come from an app."))
		return
	} else if !api && !s.asked(w, r, scheme, strings.HasPrefix(path, contract.RouteDo) || post && path == contract.RoutePush) {
		return
	}
	if name, asset := strings.CutPrefix(path, contract.RouteAsset); asset {
		s.asset(w, name)
		return
	}
	// An app's session is its header and a browser's its cookie, and neither
	// is taken in the other's place.
	secret := ""
	prefix, _ := jar(r.Context())
	if c, err := r.Cookie(prefix + s.cookie); api {
		secret = r.Header.Get(contract.HeaderApp)
	} else if err == nil {
		secret = c.Value
	}
	s.mu.Lock()
	s.caller = who
	s.mu.Unlock()
	hash := contract.TokenHash(secret)
	at := slices.IndexFunc(phones.Paired, func(p contract.Phone) bool { return secret != "" && same(p.Session, hash) })
	if at < 0 || now.Sub(phones.Paired[at].SeenAt) >= sessionLife {
		if api {
			s.stranger(w, r, who, now)
		} else {
			s.knock(w, r, who, hash, now)
		}
		return
	}
	ph := phones.Paired[at]
	if no := said("other_identity", "This phone was paired by somebody else."); ph.Identity != who && api {
		reply(w, http.StatusForbidden, no)
		return
	} else if ph.Identity != who {
		s.draw(w, http.StatusForbidden, &contract.Page{Screen: contract.ScreenRefused, Said: no})
		return
	}
	if now.Sub(ph.SeenAt) >= seenEvery && s.record(ph.ID, func(p *contract.Phone) { p.SeenAt = now }) == nil && !api {
		s.bake(w, r, secret)
	}
	r = r.WithContext(context.WithValue(r.Context(), viaPhone{}, ph.ID))
	if api {
		s.app(w, r, ph)
		return
	}
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

// bake sets a phone's cookie: the secret its session is found by. Over
// HTTPS it is one a browser sends over nothing else; scripts cannot read it
// and no other site's request carries it.
func (s *server) bake(w http.ResponseWriter, r *http.Request, secret string) {
	prefix, secure := jar(r.Context())
	// #nosec G124 -- as the session's cookie in grant: Secure at the door over HTTPS, and none over ssh's forward, plain HTTP to the machine's own name
	http.SetCookie(w, &http.Cookie{Name: prefix + s.cookie, Value: secret, Path: "/", MaxAge: int(sessionLife / time.Second),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
}

// knock answers a phone that is not paired: its number and nothing else,
// under one address. One request is open at a time; a second phone is
// refused until the first is decided or two minutes old, and after five
// refusals the next request waits, as a wrong sign-in code does.
func (s *server) knock(w http.ResponseWriter, r *http.Request, who, hash string, now time.Time) {
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
	case open && p.hash == hash && p.who == who:
	case open: // also the asker's secret under another name: a number is shown to who asked for it
		no = said("pairing_busy", "Another phone is being paired. Try again in two minutes.")
	case p.hash == hash:
		s.pair, code = pairing{}, http.StatusForbidden
		no = said("pairing_over", "That number was refused or ran out. Load this page again for a new one.")
	case s.slowed(now):
		no = pairingSlow
	default:
		secret := rand.Text()
		p = pairing{number: number(), hash: contract.TokenHash(secret), who: who, name: "a browser", at: now}
		for _, kind := range []string{"iPhone", "iPad", "Android"} {
			if strings.Contains(r.UserAgent(), kind) {
				p.name = kind
			}
		}
		s.pair, fresh = p, true
		s.bake(w, r, secret)
	}
	s.mu.Unlock()
	if fresh {
		// Written down for the panes and the terminal, which see no memory
		// of this server's; what is decided is decided here all the same.
		s.asking(&contract.Pairing{Number: p.number, Identity: p.who, Name: p.name, At: p.at})
		s.wake()
	}
	if no != nil {
		s.draw(w, code, &contract.Page{Screen: contract.ScreenRefused, Said: no})
		return
	}
	s.draw(w, http.StatusOK, &contract.Page{Screen: contract.ScreenPair, Phone: true, Pair: p.number})
}

var pairingSlow = said("pairing_slow", "Too many phones were refused: wait a little.")

// slowed reports whether pairing waits after five refusals; mu is held.
func (s *server) slowed(now time.Time) bool {
	return s.refusals >= slowFrom && now.Sub(s.refused) < time.Second<<min(s.refusals-slowFrom, slowMost)
}

// number is the six digits both screens show.
func number() string {
	var n [4]byte
	rand.Read(n[:])
	six := binary.BigEndian.Uint32(n[:]) % 1_000_000
	return fmt.Sprintf("%03d %03d", six/1000, six%1000)
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
	Name   string   `json:"name,omitempty"`
	Phones []paired `json:"phones"`
	// Asking is the phone that asks to be paired now, if one does.
	Asking *contract.Pairing `json:"asking,omitempty"`
	// Code is the pairing code for an app, as `on` has just made it.
	Code string `json:"code,omitempty"`
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
// <number>`. On a server the door needs the administrator to have set this
// login's public name; where no server is, the port the person picked. Who
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
	cfg, _ := contract.ReadPerson(k.Platform.Peek, dirs.Config)
	local := err == nil && srv == nil
	if local {
		a.Name = cmp.Or(cfg.Phone.Name, "localhost:"+strconv.Itoa(cfg.Phone.Port))
	}
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
	case word == "on" && local && cfg.Phone.Port == 0:
		return nil, refuse("no_port", "Where no server is, the page listens for a phone on a port of this machine that you pick, and none is set.", "whaleshark set phone.port 8822")
	case word == "on" && !local && (err != nil || !public):
		return nil, refuse("no_door", "This login has no door for a phone: the administrator of a server sets one.", "sudo whaleshark server phone --user "+login)
	}
	found, code := word != "forget", rand.Text()
	err = contract.ChangePhones(k.Platform, func(list *contract.Phones) error {
		if word == "on" || word == "off" {
			// A new code with every `on`, and none while the door is closed:
			// only its hash is kept.
			if list.On, list.Code = word == "on", nil; list.On {
				list.Code = &contract.PairCode{Hash: contract.TokenHash(code), Until: contract.Now().Add(contract.PairCodeLife)}
			}
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
			why := "."
			if st.Closed != "" {
				why = ": " + st.Closed + ". Another program has this login's port; end it, then switch phone access on again."
			}
			return nil, refuse("door_shut", "Phone access is on, but the door did not open"+why, "whaleshark dash status")
		}
	}
	if out.On {
		out.Name = a.Name
	}
	switch {
	case word == "on" && local:
		host, _ := os.Hostname()
		out.Code = contract.PairText(a.Name, code)
		line = fmt.Sprintf("The door for your phone is open on port %d of this machine itself, and on nothing another machine can reach.\nFrom the phone (Termius, Blink), carry a port of its own here over ssh:\n  ssh -L 8822:localhost:%[1]d %s@%s\nthen open http://localhost:8822/ in its browser, and approve the number it shows: whaleshark dash phone list\nAn app pairs with this code, good once and for one minute, and then asks for the same approval:\n  %s",
			cfg.Phone.Port, login, host, out.Code)
	case word == "on":
		out.Code = contract.PairText(a.Name, code)
		line = "The door for your phone is open. In any browser, open\n  https://" + a.Name + "/\nlog in at Cloudflare, and approve its number on the page here: whaleshark dash url\nAn app pairs with this code, good once and for one minute, and then asks for the same approval:\n  " + out.Code
	case word == "off":
		line = "The door for phones is closed. Paired phones stay paired."
	case word == "forget":
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
