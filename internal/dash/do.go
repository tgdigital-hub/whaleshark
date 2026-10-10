package dash

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// A child is a short command; one that has not ended by then is ended.
	childWait = 2 * time.Minute
	// A word a button fills into a command line is no longer than this.
	wordMost = 64
)

// child runs this same program with one command line of ours for a job,
// marked as coming from the page, and returns what it printed as JSON.
// Free text rides in a private file in the place of "{file}", never on the
// line. A command that can take minutes moves itself into a tab of its own
// and this child ends at once.
func (s *server) child(ctx context.Context, j job, text string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, childWait)
	defer cancel()
	args = slices.Clone(args)
	if at := slices.Index(args, "{file}"); at >= 0 {
		dir, err := s.k.Platform.PrivateTemp()
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		args[at] = filepath.Join(dir, "text")
		// #nosec G703 -- a file of a fixed name in a private folder made just above
		if err := os.WriteFile(args[at], []byte(text), 0o600); err != nil {
			return nil, err
		}
	}
	args = append(args, "--json", "--root", j.root, "--run", j.run)
	cmd := exec.CommandContext(ctx, s.self, args...) // #nosec G204 -- this program's own file, with a row of the action table or a read command
	cmd.Dir, cmd.Env = j.root, s.env
	out, err := cmd.Output()
	if len(out) > 0 {
		err = nil // the command said how it ended, and that is what is shown
	}
	return out, err
}

// read runs a command and reads its answer into v. A refusal is returned as
// the command's own, with its next step.
func (s *server) read(ctx context.Context, j job, v any, text string, args ...string) error {
	out, err := s.child(ctx, j, text, args...)
	if err != nil {
		return err
	}
	env := contract.Envelope{Result: v}
	switch err = json.Unmarshal(out, &env); {
	case err != nil:
		return errors.New("the command's answer could not be read")
	case env.Error != nil:
		return env.Error
	}
	return nil
}

// row is the row of the action table a button names, if the page may run
// it: a command or a fixed pointer, and none of the panes' own (going to a
// tab, the panes on and off). Nothing else can be started from a browser.
func row(id string) *contract.Action {
	for _, a := range contract.Actions {
		pages := len(a.Run) > 0 && a.Run[0] != "jump" && a.Run[0] != "ui" &&
			(a.How == contract.HowChild || a.How == contract.HowTab || a.How == contract.HowPoint)
		if a.ID == id && pages {
			return &a
		}
	}
	return nil
}

// fill puts a form's fields into a row's command line. An item's id and a
// task must be ones the view lists now, the task with this very button; any
// other word is one short line that cannot be read as an option. ok is
// false for anything else, and nothing is then run.
func fill(a *contract.Action, form url.Values, v *contract.View) (argv []string, ok bool) {
	items := slices.Concat(v.Items, v.Held, v.Answered)
	item := slices.IndexFunc(items, func(it contract.Item) bool { return it.ID == form.Get("id") })
	if a.How == contract.HowPoint {
		return a.Run, item >= 0 && slices.ContainsFunc(items[item].Buttons, func(b contract.Button) bool { return b.Action == a.ID })
	}
	for _, word := range a.Run {
		name := strings.Trim(word, "{}")
		if name == word || name == "file" {
			argv = append(argv, word)
			continue
		}
		value := form.Get(name)
		fits := value != "" && value[0] != '-' && utf8.RuneCountInString(value) <= wordMost && !strings.ContainsFunc(value, unicode.IsControl)
		switch name {
		case "id":
			fits = fits && item >= 0
		case "task":
			fits = fits && slices.ContainsFunc(v.Sections, func(sec contract.Section) bool {
				return slices.ContainsFunc(sec.Cards, func(c contract.Card) bool { return c.Task == value && slices.Contains(c.Actions, a.ID) })
			})
		}
		if !fits {
			return nil, false
		}
		argv = append(argv, value)
	}
	return argv, true
}

// do runs one button: a POST that carries the session's request token and
// names one row of the action table. What the command said is shown on the
// screen the click was made on.
func (s *server) do(w http.ResponseWriter, r *http.Request, id, button string) {
	a := row(button)
	if r.ParseForm() != nil || !same(r.PostForm.Get(contract.FieldToken), mac(s.key, "token", id)) || a == nil {
		s.draw(w, http.StatusForbidden, &contract.Page{Screen: contract.ScreenRefused, Said: said("not_a_button", "That is no button of this page.")})
		return
	}
	j, all := s.pick(w, r)
	if j.state == nil {
		s.screen(w, r, id, contract.RouteTeam, nil, nil)
		return
	}
	argv, ok := fill(a, r.PostForm, s.view(r.Context(), j, all, true))
	if !ok {
		s.draw(w, http.StatusConflict, &contract.Page{Screen: contract.ScreenRefused, Said: said("not_listed", "That button is not on the page any more: go back and look again.")})
		return
	}
	back := contract.RouteTeam
	// The way back is a path on this page and nothing a browser could read
	// as another site: no second slash, and no backslash, which browsers
	// take for one.
	if from, err := url.Parse(r.Referer()); err == nil && strings.HasPrefix(from.Path, "/") && !strings.HasPrefix(from.Path, "//") && !strings.Contains(from.Path, `\`) && !strings.HasPrefix(from.Path, contract.RouteDo) {
		back = from.Path
	}
	var err error
	var catch *contract.Catchup
	switch {
	case a.How == contract.HowPoint && j.state.Run.Orchestrator == nil:
		err = said("no_lead", "No lead agent holds this run.")
	case a.How == contract.HowPoint:
		err = s.k.Terms.Point(j.state.Run.Orchestrator.Pane, contract.Pointer(argv[0]), "")
	case a.ID == "catchup":
		catch = new(contract.Catchup)
		err = s.read(s.ctx, j, catch, "", argv...)
	default:
		// The server's own time, not the request's: a click is carried out
		// even if the tab that made it is closed meanwhile.
		err = s.read(s.ctx, j, nil, r.PostForm.Get("text"), argv...)
	}
	told := new(contract.Refusal)
	switch {
	case err == nil && catch == nil:
		http.Redirect(w, r, back, http.StatusSeeOther) // #nosec G710 -- a path of this page, checked where back is set
		return
	case err == nil:
		told = nil
	case !errors.As(err, &told):
		told = said("failed", err.Error())
	}
	s.screen(w, r, id, back, told, catch)
}
