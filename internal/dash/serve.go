package dash

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	// routeOwn is where this login's own dash command asks the running
	// server how it is, or to stop: followed by the word, with a code.
	routeOwn = "/dash/"
	// The fields that choose which of the login's jobs the page shows; the
	// choice is then kept in a cookie of that browser.
	fieldRoot, fieldRun = "root", "run"

	maxBody = 64 << 10
	policy  = "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

// server is the page's server of one login. It keeps nothing about the
// work: every screen is read from the files when it is asked for, and every
// change is a child command.
type server struct {
	k            *contract.Kit
	dirs         contract.Dirs
	self         string
	env          []string // a child's surroundings: the page's mark, and no pane and no attempt
	key          []byte
	login, host  string
	cookie, sock string
	since        time.Time
	ctx          context.Context
	stop         context.CancelFunc
	mu           sync.Mutex
	fails        int
	failed       time.Time
	swept        map[string]contract.Swept // by project folder and run
	tabs         []chan struct{}           // the browser tabs that are held open
	unlook       context.CancelFunc        // ends the looking; nil while no tab is connected
	woken, slow  bool
	ran          int
}

// job is one open run of the login with its record as just read.
type job struct {
	root, run string
	state     *contract.State
}

func (j job) id() string { return j.root + "\x00" + j.run }

// live reports whether an agent still works on a task.
func (j job) live(task string) bool {
	for _, a := range j.state.Attempts {
		if a.Task == task && a.State.Live() {
			return true
		}
	}
	return false
}

func newServer(k *contract.Kit, dirs contract.Dirs) (*server, error) {
	self, err := k.Platform.SelfPath()
	if err != nil {
		return nil, err
	}
	key, err := readKey(k.Platform, dirs, false, true)
	if err != nil {
		return nil, err
	}
	s := &server{k: k, dirs: dirs, self: self, key: key, login: contract.Me(), since: time.Now(),
		sock: filepath.Join(dirs.State, sockFile), swept: map[string]contract.Swept{}}
	s.host, _ = os.Hostname()
	// A browser shares its cookies across the ports of one host name, so
	// each login's cookie has a name of its own.
	name := sha256.Sum256([]byte(s.login))
	s.cookie = "ws" + hex.EncodeToString(name[:6])
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); name != contract.EnvPane && name != contract.EnvAttempt && name != contract.EnvFrom {
			s.env = append(s.env, kv)
		}
	}
	s.env = append(s.env, contract.EnvFrom+"="+contract.WherePage)
	s.ctx, s.stop = context.WithCancel(context.Background())
	return s, nil
}

// listen opens the one kind of door the page has: a socket file. There is
// no setting that makes it listen where another machine could reach it.
func listen(network, address string) (net.Listener, error) {
	if network != "unix" {
		return nil, refuse("no_such_door", "The page listens on its socket file and on nothing else: "+network+" "+address+" is refused.")
	}
	return net.Listen(network, address)
}

func said(code, message string) *contract.Refusal {
	return &contract.Refusal{Code: code, Message: message}
}

// ServeHTTP checks every request before anything is read for it: the host
// name, where it comes from, how it asks and who is signed in.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", policy)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	host, path, origin := r.Host, r.URL.Path, r.Header.Get("Origin")
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	post := r.Method == http.MethodPost
	rest, do := strings.CutPrefix(path, contract.RouteDo)
	switch {
	case !slices.Contains(contract.PageHosts, host):
		s.draw(w, http.StatusForbidden, &contract.Page{Screen: contract.ScreenRefused, Said: said("wrong_host", "The page answers under its own name only.")})
		return
	case origin != "" && origin != "http://"+r.Host, post && origin == "":
		s.draw(w, http.StatusForbidden, &contract.Page{Screen: contract.ScreenRefused, Said: said("wrong_origin", "That request did not come from the page.")})
		return
	case post != do || !post && r.Method != http.MethodGet && r.Method != http.MethodHead:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	now := contract.Now()
	id, signed := s.session(w, r, now)
	name, asset := strings.CutPrefix(path, contract.RouteAsset)
	word, own := strings.CutPrefix(path, routeOwn)
	file, evidence := strings.CutPrefix(path, contract.RouteEvidence)
	switch {
	case path == contract.RouteIn:
		s.in(w, r, now)
	case asset:
		if body, kind, ok := s.k.Asset(name); ok {
			h.Set("Content-Type", kind)
			w.Write(body)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	case path == contract.RouteSites:
		// connect asks this without a session: it answers port numbers and
		// is no screen, so it does not count as somebody looking.
		ports, _ := s.sites(s.open()...)
		h.Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(append([]int{}, ports...))
	case own:
		s.own(w, r, word, now)
	case !signed:
		s.draw(w, http.StatusForbidden, &contract.Page{Screen: contract.ScreenRefused, Said: said("signed_out", "Nobody is signed in in this browser."), Notes: []string{"whaleshark dash url"}})
	case do:
		s.do(w, r, id, rest)
	case path == contract.RouteLive:
		s.live(w, r)
	case evidence:
		s.evidence(w, r, file)
	default:
		s.screen(w, r, id, path, nil, nil)
	}
}

// in swaps a sign-in code for a session.
func (s *server) in(w http.ResponseWriter, r *http.Request, now time.Time) {
	switch err := s.signIn(r.URL.Query().Get(contract.FieldCode), now); {
	case err == errSlow:
		w.WriteHeader(http.StatusTooManyRequests)
	case err != nil:
		s.draw(w, http.StatusForbidden, &contract.Page{Screen: contract.ScreenRefused, Said: said("wrong_code", err.Error()), Notes: []string{"whaleshark dash url"}})
	default:
		s.grant(w, rand.Text(), now)
		http.Redirect(w, r, contract.RouteTeam, http.StatusSeeOther)
	}
}

// status is what `dash status` answers.
type status struct {
	Running bool      `json:"running"`
	Socket  string    `json:"socket,omitempty"`
	Version string    `json:"version,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	// Memory is what the server holds of the system's memory, in bytes;
	// Tabs the browser tabs held open, Watching whether it looks for news
	// (only while there is a tab), Runs the open runs its last round swept.
	Memory   uint64 `json:"memory,omitempty"`
	Tabs     int    `json:"tabs"`
	Watching bool   `json:"watching"`
	Runs     int    `json:"runs"`
}

// own answers this login's own dash command, which proves itself with a
// code signed for the one word.
func (s *server) own(w http.ResponseWriter, r *http.Request, word string, now time.Time) {
	if (word != "status" && word != "stop") || codeNumber(s.key, word, r.URL.Query().Get(contract.FieldCode), now) == 0 {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.mu.Lock()
	st := status{Running: word == "status", Socket: s.sock, Version: contract.Version, Since: s.since,
		Memory: m.Sys - m.HeapReleased, Tabs: len(s.tabs), Watching: s.unlook != nil, Runs: s.ran}
	s.mu.Unlock()
	json.NewEncoder(w).Encode(st)
	if word == "stop" {
		http.NewResponseController(w).Flush()
		s.stop()
	}
}

func (s *server) draw(w http.ResponseWriter, code int, p *contract.Page) {
	var b bytes.Buffer
	if p.Login, p.Host = s.login, s.host; s.k.Page(&b, p) != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	w.Write(b.Bytes())
}

// open lists every open run of the login's projects, each with its record.
func (s *server) open() (all []job) {
	roots, _ := contract.ReadProjects(s.k.Platform.Peek, s.dirs.State)
	for _, root := range roots {
		runs, _ := s.k.Reader().Runs(root)
		for _, run := range runs {
			if st, err := s.k.Reader().Read(root, run); err == nil && st.Run.ClosedAt.IsZero() {
				all = append(all, job{root, run, st})
			}
		}
	}
	return all
}

// pick is the job a request is about: the one its address chooses, else the
// one this browser chose last, else the first. Only a run of the login's
// own list of projects can be chosen, whatever the browser sends.
func (s *server) pick(w http.ResponseWriter, r *http.Request) (j job, all []job) {
	choice, chosen := r.URL.Query(), r.URL.Query().Has(fieldRoot)
	if c, err := r.Cookie(s.cookie + "j"); !chosen && err == nil {
		choice, _ = url.ParseQuery(c.Value)
	}
	all = s.open()
	key, fit := s.k.Platform.PathKey, 0 // 1 for any job, 2 for one of the chosen project, 3 for the chosen run
	for _, a := range all {
		is := 1
		if root := choice.Get(fieldRoot); root != "" && key(a.root) == key(root) {
			if is = 2; a.run == choice.Get(fieldRun) {
				is = 3
			}
		}
		if is > fit {
			j, fit = a, is
		}
	}
	if chosen && fit > 1 {
		http.SetCookie(w, &http.Cookie{Name: s.cookie + "j", Value: url.Values{fieldRoot: {j.root}, fieldRun: {j.run}}.Encode(),
			Path: "/", MaxAge: int(sessionLife / time.Second), HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	return j, all
}

// view builds the one view model of a job, as status does: from the record
// and the terminals' picture, both read now.
func (s *server) view(ctx context.Context, j job, all []job, every bool) *contract.View {
	now, peek := contract.Now(), s.k.Platform.Peek
	in := contract.ViewInput{Now: now, Caller: contract.Human, Watched: true, Limits: contract.ProjectDefaults()}
	if j.state == nil {
		return &contract.View{Version: contract.ViewVersion, At: now, Caller: contract.Human,
			Alerts: []string{"no open run in any project of this login"}, Fresh: contract.Fresh{Checked: now}}
	}
	var err error
	if in.Terms, err = s.k.Terms.Snapshot(ctx); err == nil {
		in.Checked = now
	}
	contract.ReadVersioned(peek, filepath.Join(s.dirs.State, contract.UIFileName), contract.FileVersion, &in.UI)
	for _, other := range all {
		if in.State = other.state; other.id() != j.id() && s.k.View(in).Counts.Waiting > 0 {
			in.Elsewhere++
		}
	}
	if limits, err := contract.ReadProjectFile(j.root); err == nil {
		in.Limits = limits
	}
	s.mu.Lock()
	if in.Swept = s.swept[j.id()]; s.slow {
		in.Notes = []string{"slow updates"}
	}
	s.mu.Unlock()
	in.State, in.Ctx, in.All = j.state, contract.ReadCtx(peek, s.dirs.State, j.state), every
	return s.k.View(in)
}

// screen draws one screen. The team's is built here; every other is the
// read command's own answer, so the page shows what the terminal would.
func (s *server) screen(w http.ResponseWriter, r *http.Request, id, path string, told *contract.Refusal, catch *contract.Catchup) {
	j, all := s.pick(w, r)
	cfg, _ := contract.ReadPerson(s.k.Platform.Peek, s.dirs.Config)
	p := &contract.Page{Screen: contract.ScreenTeam, Theme: cfg.UI.Theme, Token: mac(s.key, "token", id),
		View: s.view(r.Context(), j, all, false), Said: told, Catchup: catch}
	code, ctx := http.StatusOK, r.Context()
	var err error
	task, one := strings.CutPrefix(path, contract.RouteTask)
	switch {
	case j.state == nil:
	case path == contract.RouteTeam:
		_, p.Sites = s.sites(j)
	case path == contract.RoutePlan:
		p.Screen, p.Plan = contract.ScreenPlan, new(contract.Plan)
		err = s.read(ctx, j, p.Plan, "", "graph")
	case path == contract.RouteMessages:
		p.Screen = contract.ScreenMessages
		err = s.read(ctx, j, &p.Messages, "", "log")
	case path == contract.RouteJobs:
		p.Screen = contract.ScreenJobs
		err = s.read(ctx, j, &p.Jobs, "", "status", "--everywhere")
	case path == contract.RouteLead:
		if p.Screen = contract.ScreenLead; j.state.Run.Orchestrator != nil {
			p.Lead, err = s.k.Terms.Screen(j.state.Run.Orchestrator.Pane)
			p.Lead = strings.Map(plain, p.Lead)
		}
	case one && j.state.Tasks[task] != nil:
		p.Screen, p.Task = contract.ScreenTask, new(contract.TaskView)
		err = s.read(ctx, j, p.Task, "", "show", task)
		_, p.Sites = s.sites(j)
	default:
		code, p.Screen, p.Said = http.StatusNotFound, contract.ScreenRefused, said("no_such_screen", "The page has no such screen.")
	}
	if no := new(contract.Refusal); errors.As(err, &no) {
		p.Said = no
	} else if err != nil {
		p.Said = said("failed", err.Error())
	}
	s.draw(w, code, p)
}

// plain keeps what an agent's screen shows as text and drops what could
// steer a terminal or a browser.
func plain(r rune) rune {
	if r < ' ' && r != '\n' && r != '\t' || r == 0x7f || r >= 0x80 && r < 0xa0 {
		return -1
	}
	return r
}

// sites is the ports of the jobs' live tasks' slots that a site answers on
// now, and the lowest of them for each task.
func (s *server) sites(jobs ...job) (ports []int, by map[string]int) {
	peek := s.k.Platform.Peek
	slots, _ := contract.ReadSlots(peek, s.dirs.State)
	srv, err := contract.ReadServer(peek)
	block, ok := srv.Block(s.login)
	if err != nil || !ok {
		return nil, nil
	}
	whose := map[int]string{}
	for _, sl := range slots {
		for _, j := range jobs {
			mine := sl.Root == s.k.Platform.PathKey(j.root) && sl.Run == j.run && j.live(sl.Task)
			for port := sl.First(block); mine && port < sl.First(block)+sl.Size; port++ {
				whose[port] = sl.Task
				ports = append(ports, port)
			}
		}
	}
	ports, by = s.k.Evidence.Answering(ports), map[string]int{}
	slices.Sort(ports)
	for _, port := range slices.Backward(ports) {
		by[whose[port]] = port
	}
	return ports, by
}

// evidence serves one file an attempt saved, chosen by its place in the
// tool's own list and never by a name. Only the three kinds of picture are
// shown; anything else is a download.
func (s *server) evidence(w http.ResponseWriter, r *http.Request, file string) {
	attempt, n, _ := strings.Cut(file, "/")
	at, err := strconv.ParseUint(n, 10, 16)
	j, _ := s.pick(w, r)
	if err != nil || j.state == nil || j.state.Attempts[attempt] == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f, info, err := s.k.Evidence.Open(j.root, j.run, attempt, int(at))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer f.Close()
	if h := w.Header(); slices.Contains([]string{"png", "jpeg", "webp"}, info.Kind) {
		h.Set("Content-Type", "image/"+info.Kind)
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name}))
	}
	io.Copy(w, f)
}

// live is a browser tab's held-open connection: a line when something
// changed, for the tab to ask again, and a line every so often so that
// nothing on the way takes it for dead.
func (s *server) live(w http.ResponseWriter, r *http.Request) {
	out := http.NewResponseController(w)
	news := s.join()
	defer s.leave(news)
	w.Header().Set("Content-Type", "text/event-stream")
	beat := time.NewTicker(contract.LiveBeat)
	defer beat.Stop()
	for line := ": live"; ; {
		if _, err := fmt.Fprint(w, line, "\n\n"); err != nil || out.Flush() != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case _, held := <-news:
			if line = "data: changed"; !held {
				return
			}
		case <-beat.C:
			line = ": beat"
		}
	}
}
