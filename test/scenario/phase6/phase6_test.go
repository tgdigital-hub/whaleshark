// Package phase6 plays the whole of phase 6 once with scripted agents: a job
// with every guard off, and one flow for each guard switched on, for the
// stuck detector, for settings asked through the orchestrator and for the
// first steps. The suite of every scenario plays each file on the engine's
// double and on the real keeper and holds the two logs to each other; what
// a file's own lines cannot say is held here, with the two flows that need
// more than one project: a profile carried to another login, and an app
// that pairs by a code.
package phase6

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/test/scenario"
)

func TestMain(m *testing.M) { scenario.Main(m) }

// play runs one flow on the engine's double, with the real panes.
func play(t *testing.T, file string) *scenario.Project {
	t.Helper()
	bin, err := exec.LookPath("whaleshark")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(contract.EnvBin, bin)
	return scenario.Run(t, file, func(p *scenario.Project, name string, tm *term.Term) {
		for _, kv := range p.Env(p.Own) {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
		p.Plug(view.Plug)
		panes.Run(name, tm, p.Kit, p.Root, "")
	})
}

// login is the pretended login's own settings and what its panes keep.
func login(t *testing.T, p *scenario.Project) (contract.PersonConfig, contract.UIFile) {
	t.Helper()
	dirs, err := p.Kit.Platform.Dirs()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := contract.ReadPerson(p.Kit.Platform.Peek, dirs.Config)
	if err != nil {
		t.Fatal(err)
	}
	var ui contract.UIFile
	contract.ReadVersioned(p.Kit.Platform.Peek, filepath.Join(dirs.State, contract.UIFileName), contract.FileVersion, &ui)
	return cfg, ui
}

// kinds is every kind of event a run has had, in its inbox and its history.
func kinds(t *testing.T, p *scenario.Project, s *contract.State) (out []string) {
	t.Helper()
	history, err := p.Kit.Store.History(p.Root, s.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range append(history, s.Inbox.Events...) {
		out = append(out, e.Kind)
	}
	return out
}

// tabs is the label of every tab an agent or a person has, the panes of
// ours left out.
func tabs(t *testing.T, p *scenario.Project) (labels []string) {
	t.Helper()
	snap, err := p.Terms().Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range snap.Panes {
		if !pane.Dock && !pane.Ours {
			labels = append(labels, pane.Label)
		}
	}
	return labels
}

// With every guard off a job is as it was before phase 6: a start asks
// nobody, a protected file lands unasked, sixty agent-hours hold nothing,
// and the record has no word of a plan, a fingerprint, spending or a stuck
// agent.
func TestEveryGuardOff(t *testing.T) {
	p := play(t, "guards-off.scn")
	s, _ := p.Record()
	if s.Run.Plan != nil || s.Run.Protect != nil || s.Run.Spend != (contract.Spend{}) || len(s.Questions) != 0 || s.Run.Paused != nil {
		t.Errorf("the record of a job with every guard off: plan %+v, protect %+v, spend %+v, %d items, paused %+v", s.Run.Plan, s.Run.Protect, s.Run.Spend, len(s.Questions), s.Run.Paused)
	}
	for _, kind := range kinds(t, p, s) {
		if slices.Contains([]string{"stuck", "budget", "protected", "paused", "answered"}, kind) {
			t.Errorf("a job with every guard off had an event %q", kind)
		}
	}
	if _, err := os.Stat(contract.HoldPath(p.Root, s.Run.ID)); err == nil {
		t.Error("a hold file lies in the run's folder")
	}
	if out, _ := p.Command(scenario.Human, "status").CombinedOutput(); strings.Contains(string(out), "spent") || strings.Contains(string(out), "stuck") {
		t.Errorf("status says something of a guard:\n%s", out)
	}
}

func TestPlanFirstThenStart(t *testing.T) {
	p := play(t, "plan-first.scn")
	if s, _ := p.Record(); s.Run.Plan == nil || !slices.Equal(s.Run.Plan.Approved, []string{"T1", "T2", "T3"}) || s.Run.Plan.By == nil || s.Run.Plan.By.Caller != contract.Human {
		t.Errorf("what the person approved: %+v", s.Run.Plan)
	}
}

func TestProtectedFilesHoldTheLanding(t *testing.T) {
	p := play(t, "protected-files.scn")
	if s, _ := p.Record(); s.Run.Protect == nil || s.Run.Protect.Print == "" || s.Run.Protect.Print != s.Run.Protect.Seen || s.Run.Integration == nil || s.Run.Integration.Landed == "" {
		t.Errorf("the fingerprint the person approved and what was landed: %+v, %+v", s.Run.Protect, s.Run.Integration)
	}
}

// The hold file is gone once the person's answer has settled, and the tab
// of the agent that had reported, with nothing in hand, was closed at the
// hold: the one at work and the one at a prompt kept theirs.
func TestTheSpendingGuardHoldsAndLetsGo(t *testing.T) {
	p := play(t, "spending-guard.scn")
	s, _ := p.Record()
	if _, err := os.Stat(contract.HoldPath(p.Root, s.Run.ID)); err == nil || s.Run.Paused != nil || s.Run.Spend.More != 10 || s.Run.Spend.Held {
		t.Errorf("after ten more agent-hours: a hold file %v, paused %+v, spend %+v", err == nil, s.Run.Paused, s.Run.Spend)
	}
	if open := tabs(t, p); slices.Contains(open, "search") || !slices.Contains(open, "cart") || !slices.Contains(open, "help") {
		t.Errorf("the tabs after the hold: %q", open)
	}
}

func TestOneFleetIsHeldAlone(t *testing.T) {
	p := play(t, "fleet-held-alone.scn")
	s, _ := p.Record()
	for name, f := range s.Run.Fleets {
		if _, err := os.Stat(contract.HoldPath(p.Root, f.Run)); err == nil || f.Spend.Held || (name == "billing") != (f.Spend.More == 1) {
			t.Errorf("%s at the end: a hold file %v, spend %+v", name, err == nil, f.Spend)
		}
	}
	if s.Run.Paused != nil || s.Run.Spend.Held {
		t.Errorf("the job was held with its fleet: %+v", s.Run.Spend)
	}
}

func TestAStuckWorkerClimbsToThePerson(t *testing.T)  { play(t, "stuck-in-a-fleet.scn") }
func TestStuckByTheSameCheckAndSentBack(t *testing.T) { play(t, "stuck-by-checks.scn") }
func TestBothLeadersAreToldOfAClash(t *testing.T)     { play(t, "clash-across-fleets.scn") }

// What the orchestrator set freely is kept as its doing until the person
// has seen it, what it asked for is the person's setting once they said so,
// and what was refused is as it was.
func TestSettingsThroughTheOrchestrator(t *testing.T) {
	p := play(t, "settings-by-agent.scn")
	cfg, ui := login(t, p)
	if cfg.UI.Theme != "kelp" || !cfg.Spend.Guard || ui.DND || !slices.Equal(ui.ByAgent, []contract.SetAsk{{Key: "theme", From: "reef", To: "kelp"}}) {
		t.Errorf("the settings after the orchestrator's changes: theme %q, guard %v, dnd %v, by the agent %+v", cfg.UI.Theme, cfg.Spend.Guard, ui.DND, ui.ByAgent)
	}
	if out, err := p.Command(scenario.Human, "set", "seen", contract.SecLook).CombinedOutput(); err != nil {
		t.Fatalf("set seen: %v\n%s", err, out)
	}
	if _, ui = login(t, p); len(ui.ByAgent) != 0 {
		t.Errorf("seen, the agent's change is still flagged: %+v", ui.ByAgent)
	}
}

// All five ticked, by what happened and not by anybody's hand.
func TestTheFirstStepsTickThemselvesOff(t *testing.T) {
	p := play(t, "first-steps.scn")
	all := 1<<len(contract.FirstSteps) - 1
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		// The left section holds what it saw happen; the fifth is kept by the
		// pane that drew the list of actions.
		if _, ui := login(t, p); ui.Steps&16 != 0 && ui.FirstSteps != contract.Off {
			break
		} else if time.Now().After(end) {
			t.Fatalf("the steps kept in ui.json are %d of %d", ui.Steps, all)
		}
	}
}

// A profile saved on one pretended login and loaded on another by the real
// program: every change is shown first, the plain ones take one typed yes,
// the line that runs a program is asked by itself, and nothing changes
// without the yes.
func TestAProfileOnAnotherLogin(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mine.toml")
	run := func(t *testing.T, p *scenario.Project, typed string, args ...string) string {
		t.Helper()
		cmd := p.Command(scenario.Human, args...)
		cmd.Stdin = strings.NewReader(typed)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	t.Run("saved", func(t *testing.T) {
		p := scenario.Prepare(t, nil, nil)
		for _, line := range [][]string{{"set", "theme", "paper"}, {"set", "spend.guard", "on"}, {"set", "spend.job", "50"}, {"set", "shell", "zsh", "-l"}} {
			run(t, p, "", line...)
		}
		if out := run(t, p, "", "set", "--save", file); !strings.Contains(out, "saved: "+file) {
			t.Fatalf("set --save said:\n%s", out)
		}
	})
	t.Run("loaded", func(t *testing.T) {
		p := scenario.Prepare(t, nil, nil)
		out := run(t, p, "no\nno\n", "set", "--load", file)
		for _, want := range []string{"Colour scheme (theme): reef → paper", "Spending guard (spend.guard): off → on", "Agent-hours a job may use (spend.job): 40 → 50", "shell runs: zsh -l", "0 settings changed."} {
			if !strings.Contains(out, want) {
				t.Errorf("set --load does not say %q:\n%s", want, out)
			}
		}
		if cfg, _ := login(t, p); cfg != contract.PersonDefaults() {
			t.Fatalf("with no yes typed the settings are %+v", cfg)
		}
		// Yes to the plain ones, no to the line that runs a program.
		run(t, p, "yes\nno\n", "set", "--load", file)
		cfg, _ := login(t, p)
		dirs, _ := p.Kit.Platform.Dirs()
		engine, _ := contract.ReadSettings(p.Kit.Platform.Peek, dirs)
		if cfg.UI.Theme != "paper" || !cfg.Spend.Guard || cfg.Spend.Job != 50 || len(engine.Shell) != 0 {
			t.Errorf("loaded: theme %q, guard %v, job %d, shell %q", cfg.UI.Theme, cfg.Spend.Guard, cfg.Spend.Job, engine.Shell)
		}
	})
}

// An app at the phone's door, with the real program: it pairs by the code
// `dash phone on` prints, is nobody until the person approves its number,
// then reads the jobs and the settings, sends the lead agent a message and
// changes a setting that holds agents, which every screen flags; its address
// for a nudge is answered with what that waits for; the code is good once.
func TestAnAppPairsByACodeAndIsUsed(t *testing.T) {
	b64 := base64.RawURLEncoding
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp4", net.JoinHostPort(contract.Loopback, "0"))
	if err != nil {
		t.Fatal(err)
	}
	keys := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "k1", "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": b64.EncodeToString(key.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})}
	go keys.Serve(l)
	t.Cleanup(func() { keys.Close() })
	_, port, _ := net.SplitHostPort(l.Addr().String())

	f, err := testkit.Load(testkit.Evening)
	if err != nil {
		t.Fatal(err)
	}
	p := scenario.Prepare(t, f, nil)
	base, release, err := testkit.Block()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	const name, me = "ws1.example.org", "robin@example.org"
	p.Server(`road = "cloudflare"`, fmt.Sprintf("team = %q", port), "[login.{me}]", fmt.Sprint("ports = ", base), fmt.Sprintf("name = %q", name), `aud = "tag-of-the-test"`, fmt.Sprintf("email = [%q]", me))
	ask := func(v any, args ...string) bool {
		out, _ := p.Command(scenario.Unbound, append(args, "--json")...).Output()
		env := contract.Envelope{Result: v}
		return json.Unmarshal(out, &env) == nil && env.OK
	}
	server := p.Command(scenario.Unbound, "dash", "start")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Command(scenario.Unbound, "dash", "stop").Run()
		server.Process.Kill()
		server.Wait()
	})
	var st struct{ Running bool }
	for end := time.Now().Add(20 * time.Second); !st.Running; time.Sleep(50 * time.Millisecond) {
		if ask(&st, "dash", "status"); time.Now().After(end) {
			t.Fatal("the page's server did not start")
		}
	}
	var on struct{ Code string }
	if !ask(&on, "dash", "phone", "on") || !strings.HasPrefix(on.Code, contract.PairText(name, "")) {
		t.Fatalf("dash phone on gave the code %q", on.Code)
	}
	word := strings.TrimPrefix(on.Code, contract.PairText(name, ""))

	now := f.Now.Unix() // the scenario's clock, which the door reads
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body, _ := json.Marshal(map[string]any{"iss": "http://" + net.JoinHostPort(contract.Loopback, port), "aud": []string{"tag-of-the-test"}, "type": "app", "email": me, "sub": "subject-1",
		"exp": now + 600, "iat": now, "nbf": now})
	signed := b64.EncodeToString(head) + "." + b64.EncodeToString(body)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	const secret = "a-secret-the-app-made-itself"
	// The app is no browser: no cookie, no origin, its secret in a header.
	app := func(method, path, sent string, paired bool) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, "http://"+net.JoinHostPort(contract.Loopback, strconv.Itoa(base+contract.BlockSize-1))+path, strings.NewReader(sent))
		req.Host = name
		req.Header.Set(contract.HeaderAccess, signed+"."+b64.EncodeToString(sig))
		if paired {
			req.Header.Set(contract.HeaderApp, secret)
		}
		resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		text, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(text)
	}
	pairing, _ := json.Marshal(contract.AppPair{Word: word, Session: contract.TokenHash(secret), Name: "a test app"})
	code, said := app("POST", contract.APIv1+contract.APIPair, string(pairing), false)
	var shown struct{ Number string }
	if json.Unmarshal([]byte(said), &shown); code != http.StatusOK || len(shown.Number) != 7 {
		t.Fatalf("pairing by the code: %d %s", code, said)
	}
	if code, said := app("GET", contract.APIv1+contract.APIView, "", true); code != http.StatusForbidden {
		t.Fatalf("an app nobody approved reads the jobs: %d %s", code, said)
	}
	if out, err := p.Command(scenario.Unbound, "dash", "phone", "approve", strings.ReplaceAll(shown.Number, " ", "")).CombinedOutput(); err != nil {
		t.Fatalf("approving the app's number: %v\n%s", err, out)
	}
	var jobs []contract.Job
	if code, said := app("GET", contract.APIv1+contract.APIView, "", true); code != http.StatusOK || json.Unmarshal([]byte(said), &jobs) != nil || len(jobs) != 1 || jobs[0].View.Run != f.State.Run.ID {
		t.Fatalf("the jobs as the app reads them: %d %.300s", code, said)
	}
	if code, said := app("GET", contract.APIv1+contract.APISettings, "", true); code != http.StatusOK || !strings.Contains(said, `"spend.guard"`) || !strings.Contains(said, `"`+contract.SetAsks+`"`) {
		t.Errorf("the settings as the app reads them: %d %.300s", code, said)
	}
	s, _ := p.Record()
	events := len(s.Inbox.Events)
	// Typed into the lead agent's tab as the app's holder's line, or, with
	// the agent not at rest in that instant, waiting for it: the app is told which.
	if code, said := app("POST", contract.APIv1+contract.APITell, `{"text":"the price list may go"}`, true); code != http.StatusNoContent && (code != http.StatusAccepted || !strings.Contains(said, `"chat":true`)) {
		t.Errorf("a message to the lead agent: %d %s", code, said)
	}
	if s, _ = p.Record(); len(s.Inbox.Events) != events+1 || !strings.Contains(s.Inbox.Events[events].Text, "the price list may go") {
		t.Errorf("the message did not reach the lead agent's inbox: %+v", s.Inbox.Events[events:])
	}
	if code, said := app("POST", contract.APIv1+contract.APISet, `{"key":"stuck.climb_minutes","value":"15"}`, true); code != http.StatusNoContent {
		t.Errorf("a setting that holds agents, from the app: %d %s", code, said)
	}
	if code, said := app("POST", contract.APIv1+contract.APISet, `{"key":"shell","value":"sh"}`, true); code == http.StatusNoContent {
		t.Errorf("a setting that names a program, from the app: %d %s", code, said)
	}
	cfg, ui := login(t, p)
	out, _ := p.Command(scenario.Human, "status").CombinedOutput()
	if cfg.Stuck.ClimbMinutes != 15 || !slices.Equal(ui.Changed, []string{"stuck.climb_minutes"}) || !strings.Contains(string(out), "setting changed: stuck.climb_minutes") {
		t.Errorf("the app's change: %d minutes, flagged %q, and status says:\n%s", cfg.Stuck.ClimbMinutes, ui.Changed, out)
	}
	if code, said := app("POST", contract.APIv1+contract.APIPush, `{"endpoint":"https://push.example/x","p256dh":"k","auth":"a"}`, true); code != http.StatusConflict || !strings.Contains(said, "waits_for_the_apps") {
		t.Errorf("the app's address for a nudge: %d %s", code, said)
	}
	// The word is spent: a second app with it is nobody.
	other, _ := json.Marshal(contract.AppPair{Word: word, Session: contract.TokenHash("another-secret"), Name: "a second app"})
	if code, said := app("POST", contract.APIv1+contract.APIPair, string(other), false); code == http.StatusOK {
		t.Errorf("the code was good twice: %s", said)
	}
}
