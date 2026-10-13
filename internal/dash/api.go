package dash

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The interface for apps: what the page shows a paired phone, as data, at
// the door and nowhere else. An answer is a shape of the contract under 200,
// nothing under 204, or a contract.Refusal under the status that says why.
// Nothing here writes or runs anything of its own: what changes something
// is a row of the action table, carried out by press as a button is.

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// fail answers what a command or the server said no with.
func fail(w http.ResponseWriter, err error) {
	no := new(contract.Refusal)
	if !errors.As(err, &no) {
		no = said("failed", err.Error())
	}
	reply(w, http.StatusConflict, no)
}

func root(paired bool) contract.AppRoot {
	v := contract.APIVersions
	return contract.AppRoot{Versions: v, Newest: v[len(v)-1], Program: contract.Version, Paired: paired}
}

// stranger answers a caller with the road's pass and no session: which
// versions are served, and a request to pair by the code.
func (s *server) stranger(w http.ResponseWriter, r *http.Request, who string, now time.Time) {
	switch post := r.Method == http.MethodPost; {
	case r.URL.Path == contract.RouteAPI && r.Method == http.MethodGet:
		reply(w, http.StatusOK, root(false))
	case r.URL.Path == contract.APIv1+contract.APIPair && post:
		s.code(w, r, who, now)
	default:
		reply(w, http.StatusForbidden, said("not_paired", "This app is not paired."))
	}
}

// code takes the one-time word with the hash of a secret the app made, and
// pairs nothing: the app becomes the one phone that asks, under a number
// the person approves at the computer. A word is spent by its first use, a
// wrong one counts as a refusal, and five of those slow pairing as ever.
func (s *server) code(w http.ResponseWriter, r *http.Request, who string, now time.Time) {
	var p contract.AppPair
	no, status := said("bad_request", "Pairing takes a word, the hash of a secret and a name."), http.StatusBadRequest
	fits := json.NewDecoder(r.Body).Decode(&p) == nil && len(p.Session) == len(contract.TokenHash("")) &&
		strings.HasPrefix(p.Session, "sha256:") && p.Session != contract.TokenHash("")
	name := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			return r
		}
		return -1
	}, p.Name))
	if name == "" || utf8.RuneCountInString(name) > 24 {
		name = "an app"
	}
	s.mu.Lock()
	switch good := false; {
	case !fits:
	case s.slowed(now):
		no, status = pairingSlow, http.StatusTooManyRequests
	case s.pair.hash != "" && now.Sub(s.pair.at) < pairLife:
		no, status = said("pairing_busy", "Another phone is being paired. Try again in two minutes."), http.StatusTooManyRequests
	case contract.ChangePhones(s.k.Platform, func(list *contract.Phones) error {
		if good = list.Code.Good(p.Word, now); good {
			list.Code = nil
		}
		return nil
	}) != nil || !good:
		s.refusals, s.refused = s.refusals+1, now
		no, status = said("wrong_code", "That pairing code is not good: it was used, or its minute is over."), http.StatusForbidden
	default:
		no, s.pair = nil, pairing{number: number(), hash: p.Session, who: who, name: name, at: now}
	}
	pair := s.pair
	s.mu.Unlock()
	if no != nil {
		reply(w, status, no)
		return
	}
	s.asking(&contract.Pairing{Number: pair.number, Identity: who, Name: name, At: now, Session: pair.hash})
	s.wake()
	reply(w, http.StatusOK, map[string]string{"number": pair.number})
}

// named is the open job an app names by its run, and by its project where
// two projects have a run of that name; with no name, the first.
func (s *server) named(run, root string) (j job, all []job) {
	all = s.open()
	for _, a := range all {
		if (run == "" || a.run == run) && (root == "" || s.k.Platform.PathKey(a.root) == s.k.Platform.PathKey(root)) {
			return a, all
		}
	}
	return job{}, all
}

// app answers a paired app.
func (s *server) app(w http.ResponseWriter, r *http.Request, ph contract.Phone) {
	path, ctx, q := strings.TrimPrefix(r.URL.Path, contract.APIv1), r.Context(), r.URL.Query()
	task, one := strings.CutPrefix(path, contract.APITask)
	chat, screen := strings.CutPrefix(path, contract.APIChat)
	button, do := strings.CutPrefix(path, contract.APIDo)
	posts := do || path == contract.APITell || path == contract.APISet || path == contract.APIPush
	if post := r.Method == http.MethodPost; post != posts || !post && r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	run, id := q.Get(contract.FieldRun), ""
	if one {
		run, id, _ = strings.Cut(task, "/")
	} else if screen {
		run, id, _ = strings.Cut(chat, "/")
	}
	j, all := s.named(run, q.Get(contract.FieldRoot))
	var a *contract.Action
	var form url.Values
	switch {
	case path == contract.RouteAPI:
		reply(w, http.StatusOK, root(true))
	case path == contract.APIView:
		jobs := []contract.Job{}
		for _, j := range all {
			jobs = append(jobs, contract.Job{Root: j.root, View: s.view(ctx, j, all, true)})
		}
		reply(w, http.StatusOK, jobs)
	case path == contract.APILive:
		s.live(w, r)
	case path == contract.APISettings:
		list := []contract.AppSetting{}
		for _, k := range contract.Keys {
			v := contract.AppSetting{Key: k, Value: contract.Current(s.k.Platform, s.dirs, k.Name), Agent: k.Agent()}
			// What is the person's alone names a program or an address that
			// reaches outside: an app is shown that it is set, never to what.
			if v.Agent == contract.SetPerson && v.Value != "" {
				v.Value = contract.Dots
			}
			if k.Section != "" {
				list = append(list, v)
			}
		}
		reply(w, http.StatusOK, list)
	case path == contract.APIPush:
		// The address is version 1's and says plainly what it waits for: an
		// app's push needs the relay, and nothing is kept until there is one.
		reply(w, http.StatusConflict, said("waits_for_the_apps", "An app is not nudged yet: that takes a relay for the apps' own push, which is not built. Nothing was kept. While the app is open, "+contract.APIv1+contract.APILive+" says when the view changes."))
	case !posts && !one && !screen:
		reply(w, http.StatusNotFound, said("no_such_route", "Version 1 has no such route."))
	case j.state == nil:
		reply(w, http.StatusNotFound, said("no_such_job", "No open job of this login is called that."))
	case screen && id == "lead" && j.state.Run.Orchestrator != nil:
		text, err := s.k.Terms.Screen(j.state.Run.Orchestrator.Pane)
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusOK, lines(j.run, id, text))
	case (one || screen) && j.state.Tasks[id] == nil:
		reply(w, http.StatusNotFound, said("no_such_task", "That job has no such task."))
	case one, screen:
		tv, args := new(contract.TaskView), []string{"show", id}
		if screen {
			args = append(args, "--screen")
		}
		if err := s.read(ctx, j, tv, "", args...); err != nil {
			fail(w, err)
		} else if screen {
			reply(w, http.StatusOK, lines(j.run, id, tv.Screen))
		} else {
			reply(w, http.StatusOK, tv)
		}
	default:
		// A message and a setting are rows of the same table, with the
		// fields a form would have carried.
		var tell contract.AppTell
		var set contract.AppSet
		switch {
		case do && r.ParseForm() == nil:
			a, form = row(button), r.PostForm
		case path == contract.APITell && json.NewDecoder(r.Body).Decode(&tell) == nil && tell.Text != "" && len(tell.Text) <= contract.TellMax:
			a, form = row("note-lead"), url.Values{"text": {tell.Text}}
		case path == contract.APISet && json.NewDecoder(r.Body).Decode(&set) == nil:
			a, form = row("set"), url.Values{"key": {set.Key}, "value": {set.Value}}
		}
		if a == nil {
			reply(w, http.StatusForbidden, said("not_a_button", "That is no row of the action table an app may press, no message of at most 1 MB and no setting."))
			return
		}
		var told contract.AppTold
		if catch, err := s.press(ctx, a, form, j, all, &told); err != nil {
			fail(w, err)
		} else if catch != nil {
			reply(w, http.StatusOK, catch)
		} else if told.Chat && told.Why != "" {
			reply(w, http.StatusAccepted, told) // the note waits for the lead agent to rest
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// lines is an agent's screen as text, the last of it, without what could
// steer a terminal.
func lines(run, who, text string) contract.AppChat {
	all := strings.Split(strings.TrimRight(strings.Map(plain, text), "\n"), "\n")
	return contract.AppChat{Run: run, Who: who, Lines: all[max(0, len(all)-contract.ChatLines):]}
}
