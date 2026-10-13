package dash

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// An app is a program that is no browser: it holds no cookie, names no
// origin and sends the secret it made in a header. Cloudflare's connector
// hands its requests to the door with a pass, as it hands over a phone's.
type app struct {
	b           *browser
	who, secret string
}

func (a *app) ask(method, path, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	a.b.t.Helper()
	r := httptest.NewRequest(method, "https://"+public+path, strings.NewReader(body))
	if a.who != "" {
		r.Header.Set(contract.HeaderAccess, signed(cloud(), heading, set("sub", a.who)))
	}
	if a.secret != "" {
		r.Header.Set(contract.HeaderApp, a.secret)
	}
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	a.b.s.phone(w, r)
	return w
}

// into reads an answer that must be good into v.
func (a *app) into(path string, v any) {
	a.b.t.Helper()
	if w := a.ask("GET", path, "", nil); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), v) != nil {
		a.b.t.Fatalf("%s: %d %.300s", path, w.Code, w.Body)
	}
}

// no holds an answer to be a refusal of that status and code, as JSON.
func (a *app) no(what string, w *httptest.ResponseRecorder, status int, code string) {
	a.b.t.Helper()
	var said contract.Refusal
	if json.Unmarshal(w.Body.Bytes(), &said); w.Code != status || said.Code != code || len(w.Result().Cookies()) != 0 {
		a.b.t.Errorf("%s: answered %d %q, not %d %q: %.200s", what, w.Code, said.Code, status, code, w.Body)
	}
}

// code puts a pairing code into phones.json as `dash phone on` does, good
// for life from now, and returns its word.
func (b *browser) code(life time.Duration) string {
	b.t.Helper()
	word := rand.Text()
	if err := contract.ChangePhones(b.p.Kit.Platform, func(list *contract.Phones) error {
		list.Code = &contract.PairCode{Hash: contract.TokenHash(word), Until: contract.Now().Add(life)}
		return nil
	}); err != nil {
		b.t.Fatal(err)
	}
	return word
}

// present hands the door a word with the hash of the app's own secret.
func (a *app) present(word string) *httptest.ResponseRecorder {
	a.b.t.Helper()
	if a.secret == "" {
		a.secret = rand.Text()
	}
	body, _ := json.Marshal(contract.AppPair{Word: word, Session: contract.TokenHash(a.secret), Name: "iPhone"})
	secret := a.secret
	a.secret = "" // it has no session yet
	w := a.ask("POST", contract.APIv1+contract.APIPair, string(body), nil)
	a.secret = secret
	return w
}

// numbered is the number the app was answered with, without its space.
func numbered(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var got map[string]string
	if json.Unmarshal(w.Body.Bytes(), &got); w.Code != http.StatusOK || len(got["number"]) != 7 {
		t.Fatalf("presenting a code: %d %s", w.Code, w.Body)
	}
	return strings.ReplaceAll(got["number"], " ", "")
}

// installed is an app the person has approved.
func (b *browser) installed(who string) *app {
	b.t.Helper()
	a := &app{b: b, who: who}
	if w := b.decide(numbered(b.t, a.present(b.code(time.Minute))), "approve"); w.Code != http.StatusSeeOther {
		b.t.Fatalf("approving: %d %s", w.Code, w.Body)
	}
	return a
}

// The whole of what an app is for: it pairs with a code, reads the view, a
// task, a chat and the settings, presses a button, sends a message and
// changes a setting, and each is recorded as the person's, from a phone.
func TestAnAppFromItsCodeToAMessage(t *testing.T) {
	b := house(t)
	b.s.k.Terms = screens{b.s.k.Terms, "one\x1b[31m two\x07\n> done\n"}
	a := &app{b: b, who: subject}
	var top contract.AppRoot
	if a.into(contract.RouteAPI, &top); !reflect.DeepEqual(top, contract.AppRoot{Versions: []int{1}, Newest: 1, Program: contract.Version}) {
		t.Fatalf("the versions, to a stranger: %+v", top)
	}
	number := numbered(t, a.present(b.code(time.Minute)))
	asking := listOf(t, b).Asking
	if asking == nil || strings.ReplaceAll(asking.Number, " ", "") != number || asking.Name != "iPhone" || asking.Identity != named || asking.Session != contract.TokenHash(a.secret) {
		t.Fatalf("the app that asks, as the screens read it: %+v", asking)
	}
	if text := b.ask("GET", contract.RouteTeam, nil, nil).Body.String(); !strings.Contains(text, asking.Number+" · "+named+" · iPhone") {
		t.Errorf("the laptop's page does not show the app that asks:\n%.400s", text)
	}
	a.no("the view before the person approved", a.ask("GET", contract.APIv1+contract.APIView, "", nil), http.StatusForbidden, "not_paired")
	if a.into(contract.RouteAPI, &top); top.Paired {
		t.Fatal("the app is paired before the person approved its number")
	}
	b.refused("approving", b.decide(number, "approve"), http.StatusSeeOther)
	list := listOf(t, b)
	if len(list.Paired) != 1 || list.Paired[0].Session != contract.TokenHash(a.secret) || list.Paired[0].Identity != named || list.Paired[0].Name != "iPhone" || list.Code != nil {
		t.Fatalf("the app as paired: %+v", list)
	}
	if data, _ := os.ReadFile(filepath.Join(b.s.dirs.State, "phones.json")); strings.Contains(string(data), a.secret) {
		t.Error("the app's secret is in the file")
	}
	if a.into(contract.RouteAPI, &top); !top.Paired {
		t.Fatal("the app is not paired after the approval")
	}

	s, _ := b.p.Record()
	var jobs []contract.Job
	a.into(contract.APIv1+contract.APIView, &jobs)
	if len(jobs) != 1 || b.s.k.Platform.PathKey(jobs[0].Root) != b.s.k.Platform.PathKey(b.p.Root) || jobs[0].View.Run != s.Run.ID || len(jobs[0].View.Sections) == 0 || len(jobs[0].View.Items) == 0 {
		t.Fatalf("the view: %+v", jobs)
	}
	for _, it := range append(jobs[0].View.Items, jobs[0].View.Answered...) {
		if strings.Contains(it.Text, "phone") {
			t.Errorf("an app is shown what the login's phones ask: %q", it.Text)
		}
	}
	var task contract.TaskView
	if a.into(contract.APIv1+contract.APITask+s.Run.ID+"/T8", &task); task.Card.Task != "T8" || task.Run != s.Run.ID {
		t.Errorf("a task: %+v", task)
	}
	var chat contract.AppChat
	if a.into(contract.APIv1+contract.APIChat+s.Run.ID+"/lead", &chat); !reflect.DeepEqual(chat, contract.AppChat{Run: s.Run.ID, Who: "lead", Lines: []string{"one[31m two", "> done"}}) {
		t.Errorf("the lead agent's chat: %+v", chat)
	}
	var settings []contract.AppSetting
	a.into(contract.APIv1+contract.APISettings, &settings)
	kinds := map[string]int{}
	for _, row := range settings {
		if kinds[row.Agent]++; row.Label == "" || row.Section == "" || row.Agent == contract.SetPerson && row.Value != "" && row.Value != contract.Dots {
			t.Errorf("a setting as an app reads it: %+v", row)
		}
	}
	if len(settings) < 50 || kinds[contract.SetFree] == 0 || kinds[contract.SetAsks] == 0 || kinds[contract.SetPerson] == 0 {
		t.Errorf("the settings: %d, of the kinds %v", len(settings), kinds)
	}

	post := func(path, body string) *httptest.ResponseRecorder {
		return a.ask("POST", contract.APIv1+path, body, func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") })
	}
	b.refused("a button", post(contract.APIDo+"answer", url.Values{"id": {"q9"}, "text": {"three sizes"}}.Encode()), http.StatusNoContent)
	events := len(s.Inbox.Events)
	// A whole megabyte, and one that begins like an option: it is the message.
	b.refused("a message", post(contract.APITell, `{"text":"--now `+strings.Repeat("a", contract.TellMax-6)+`"}`), http.StatusNoContent)
	// It was typed into the lead agent's tab as the phone's line. The next
	// finds the agent at work on it: it waits, and the app is told so.
	if w := post(contract.APITell, `{"text":"and one more"}`); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"chat":true,"why":"the agent is at work`) {
		t.Errorf("a message that waits: %d %s", w.Code, w.Body)
	}
	b.refused("a setting", post(contract.APISet, `{"key":"theme","value":"kelp"}`), http.StatusNoContent)
	// A key that holds agents or calls the person is the holder's own act
	// from an app, and flagged on every screen; a harmless one is not.
	b.refused("a setting that calls the person", post(contract.APISet, `{"key":"mute","value":"on"}`), http.StatusNoContent)
	b.refused("a setting that holds agents", post(contract.APISet, `{"key":"stuck.climb_minutes","value":"15"}`), http.StatusNoContent)
	b.refused("do not disturb", post(contract.APISet, `{"key":"dnd","value":"on"}`), http.StatusNoContent)
	var ui contract.UIFile
	if d, _ := b.p.Kit.Platform.Dirs(); contract.ReadVersioned(os.ReadFile, filepath.Join(d.State, contract.UIFileName), contract.FileVersion, &ui) != nil ||
		!ui.Mute || !ui.DND || !slices.Equal(ui.Changed, []string{"mute", "stuck.climb_minutes", "dnd"}) || contract.Warned(ui.Changed) != "setting changed: mute, stuck.climb_minutes, dnd" {
		t.Errorf("ui.json after an app's settings: %+v, warned %q", ui, contract.Warned(ui.Changed))
	}
	s, _ = b.p.Record()
	if q := s.Questions["q9"]; q.Answer != "three sizes" || q.AnsweredBy == nil || q.AnsweredBy.Where != contract.WherePhone || q.AnsweredBy.Caller != contract.Human {
		t.Errorf("the answer as recorded: %q by %+v", q.Answer, q.AnsweredBy)
	}
	if len(s.Inbox.Events) <= events {
		t.Error("the message reached no agent")
	}
	if got := contract.Current(b.p.Kit.Platform, b.s.dirs, "theme"); got != "kelp" {
		t.Errorf("the theme after the app set it: %q", got)
	}
	var catch contract.Catchup
	if w := post(contract.APIDo+"catchup", ""); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &catch) != nil {
		t.Errorf("catch me up: %d %.200s", w.Code, w.Body)
	}

	// The held-open line says that something changed and never what.
	b.s.join() // a tab's place, so that the news has somebody to go to
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- a.ask("GET", contract.APIv1+contract.APILive, "", nil) }()
	until(t, "the app listening", func() bool { b.s.mu.Lock(); defer b.s.mu.Unlock(); return len(b.s.tabs) == 2 })
	b.s.stop()
	if w := <-done; w.Header().Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(w.Body.String(), ": live\n\n") {
		t.Errorf("the live line: %q", w.Body)
	}
}

// What an app never may, in version 1 or later: type into a tab, run a
// command, set what names a program, approve trust, pair or forget a
// phone, read a file of a project or evidence. Each is refused and nothing
// is changed; the same holds of a phone's page for a setting.
func TestWhatAnAppNeverMay(t *testing.T) {
	b := house(t)
	a := b.installed(subject)
	s, _ := b.p.Record()
	form := func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }
	do := func(button string, fields ...string) *httptest.ResponseRecorder {
		v := url.Values{}
		for i := 0; i < len(fields); i += 2 {
			v.Set(fields[i], fields[i+1])
		}
		return a.ask("POST", contract.APIv1+contract.APIDo+url.PathEscape(button), v.Encode(), form)
	}
	for _, never := range []string{"land", "trust", "run", "task", "report", "mail", "go", "fleet", "actions", "to-actions", "menu", "show", "export", "show-changes", "no-such", "", "answer/../land"} {
		a.no("the button "+never, do(never, "task", "T8", "id", "q9"), http.StatusForbidden, "not_a_button")
	}
	// A second phone asks: an app neither approves it nor forgets the first.
	other := &phone{b: b, who: subject}
	number := other.knock()
	a.no("pairing another phone", do("phone-yes", "id", "pair"+number), http.StatusConflict, "not_listed")
	a.no("forgetting a phone", do("forget-phone", "id", "ph1"), http.StatusConflict, "not_listed")
	b.refused("refusing the second phone", b.decide(number, "refuse"), http.StatusSeeOther)
	a.no("an item nobody listed", do("answer", "id", "q99", "text", "yes"), http.StatusConflict, "not_listed")
	a.no("a task that is an option", do("stop", "task", "--everywhere"), http.StatusConflict, "not_listed")
	a.no("a button its card does not show", do("accept", "task", "T7"), http.StatusConflict, "not_listed")
	set := func(key, value string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(contract.AppSet{Key: key, Value: value})
		return a.ask("POST", contract.APIv1+contract.APISet, string(body), nil)
	}
	for _, key := range []string{"shell", "notify.url", "tools.hermes.run", "tools.hermes.env.TOKEN", "server.road", "seen", "draft.q9", "no.such.key"} {
		a.no("setting "+key, set(key, "sh"), http.StatusConflict, "not_from_a_phone")
		a.no("the button for "+key, do("set", "key", key, "value", "sh"), http.StatusConflict, "not_from_a_phone")
	}
	a.no("a setting that is an option", set("theme", "--root=/"), http.StatusConflict, "not_listed")
	a.no("a message of nothing", a.ask("POST", contract.APIv1+contract.APITell, `{"text":""}`, nil), http.StatusForbidden, "not_a_button")
	a.no("a message of more than a megabyte", a.ask("POST", contract.APIv1+contract.APITell, `{"text":"`+strings.Repeat("a", contract.TellMax+1)+`"}`, nil), http.StatusForbidden, "not_a_button")
	a.no("a message that is no JSON", a.ask("POST", contract.APIv1+contract.APITell, "text=hello", nil), http.StatusForbidden, "not_a_button")
	// A phone's own page sets no more than an app does.
	p := b.paired(subject)
	b.refused("a phone's page setting a program", p.ask("POST", contract.RouteDo+"set", url.Values{contract.FieldToken: {p.token()}, "key": {"shell"}, "value": {"sh"}}, nil), http.StatusConflict)

	for _, path := range []string{"/evidence/T8.1/0", "/file/go.mod", "/keys", "/pair", "/plan", "/../team", "/task", "/view/", "/2/view"} {
		if w := a.ask("GET", contract.APIv1+path, "", nil); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed || strings.Contains(w.Body.String(), "<") {
			t.Errorf("%s: %d %.200s", path, w.Code, w.Body)
		}
	}
	a.no("a task of no job", a.ask("GET", contract.APIv1+contract.APITask+"r999/T8", "", nil), http.StatusNotFound, "no_such_job")
	a.no("a task nobody listed", a.ask("GET", contract.APIv1+contract.APITask+s.Run.ID+"/T99", "", nil), http.StatusNotFound, "no_such_task")
	a.no("a chat that is an option", a.ask("GET", contract.APIv1+contract.APIChat+s.Run.ID+"/--diff", "", nil), http.StatusNotFound, "no_such_task")
	for _, path := range []string{contract.APIView, contract.APISettings, contract.APITask + s.Run.ID + "/T8"} {
		if w := a.ask("POST", contract.APIv1+path, "", nil); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("a POST to %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{contract.APITell, contract.APISet, contract.APIDo + "stop-all", contract.APIPush} {
		if w := a.ask("GET", contract.APIv1+path, "", nil); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("a GET of %s: %d", path, w.Code)
		}
	}
	after, _ := b.p.Record()
	if list := listOf(t, b); len(list.Paired) != 2 || len(after.Inbox.Events) != len(s.Inbox.Events) || after.Questions["q9"].State != contract.QuestionOpen ||
		contract.Current(b.p.Kit.Platform, b.s.dirs, "shell") == "sh" {
		t.Errorf("a refusal changed something: %d phones, %d events", len(list.Paired), len(after.Inbox.Events))
	}
}

// The door's attacks, against the interface: another site in the person's
// own browser, a neighbour on the machine without a pass, a caller who was
// never paired, somebody else's pass with this app's secret, and a phone
// that was forgotten. A browser's cookie opens nothing of the interface
// and an app's header nothing of the page.
func TestTheInterfaceBehindTheDoorsWalls(t *testing.T) {
	b := house(t)
	a := b.installed(subject)
	s, _ := b.p.Record()
	routes := []string{contract.RouteAPI, contract.APIv1 + contract.APIView, contract.APIv1 + contract.APISettings, contract.APIv1 + contract.APILive,
		contract.APIv1 + contract.APITask + s.Run.ID + "/T8", contract.APIv1 + contract.APIChat + s.Run.ID + "/lead"}
	posts := []string{contract.APIv1 + contract.APITell, contract.APIv1 + contract.APISet, contract.APIv1 + contract.APIDo + "stop-all", contract.APIv1 + contract.APIPair, contract.APIv1 + contract.APIPush}
	each := func(what string, by *app, change func(*http.Request), check func(string, *httptest.ResponseRecorder)) {
		t.Helper()
		for _, path := range routes {
			check(what+" reading "+path, by.ask("GET", path, "", change))
		}
		for _, path := range posts {
			check(what+" posting to "+path, by.ask("POST", path, `{"text":"hello","key":"theme","value":"kelp"}`, change))
		}
	}
	each("a neighbour without a pass", &app{b: b, secret: a.secret}, nil, func(what string, w *httptest.ResponseRecorder) { nothing(t, what, w) })
	each("another site", a, func(r *http.Request) { r.Header.Set("Origin", "https://elsewhere.example") }, func(what string, w *httptest.ResponseRecorder) {
		a.no(what, w, http.StatusForbidden, "wrong_origin")
	})
	// What another site can send without the browser asking first carries no header of ours.
	stranger := &app{b: b, who: subject}
	for _, path := range append(routes[1:], posts[:3]...) {
		method := map[bool]string{true: "GET", false: "POST"}[strings.Contains(strings.Join(routes, " "), path)]
		a.no("a caller never paired at "+path, stranger.ask(method, path, `{"text":"hello"}`, nil), http.StatusForbidden, "not_paired")
		a.no("a wrong secret at "+path, (&app{b: b, who: subject, secret: rand.Text()}).ask(method, path, "", nil), http.StatusForbidden, "not_paired")
		a.no("somebody else's pass at "+path, (&app{b: b, who: kim, secret: a.secret}).ask(method, path, "", nil), http.StatusForbidden, "other_identity")
	}
	if w := a.ask("OPTIONS", contract.APIv1+contract.APITell, "", func(r *http.Request) { r.Header.Set("Access-Control-Request-Headers", contract.HeaderApp) }); w.Code != http.StatusMethodNotAllowed || len(w.Header().Values("Access-Control-Allow-Origin")) != 0 {
		t.Errorf("a browser asking first whether it may send the header: %d %v", w.Code, w.Header())
	}

	// Each road's session is nobody's on the other.
	p := b.paired(subject)
	cookie := func(r *http.Request) { r.AddCookie(p.cookie[0]) }
	a.no("a browser's cookie at the interface", stranger.ask("GET", contract.APIv1+contract.APIView, "", cookie), http.StatusForbidden, "not_paired")
	if w := a.ask("GET", contract.RouteTeam, "", nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != contract.RoutePair {
		t.Errorf("an app's header at the page: %d %.100s", w.Code, w.Body)
	}
	if w := b.ask("GET", contract.APIv1+contract.APIView, nil, func(r *http.Request) { r.Header.Set(contract.HeaderApp, a.secret) }); strings.Contains(w.Body.String(), `"sections"`) {
		t.Errorf("the laptop's road serves the interface: %d %.200s", w.Code, w.Body)
	}

	// Forgotten, the app has no session; so has one not seen for thirty days.
	if err := b.s.record("ph1", func(ph *contract.Phone) { ph.SeenAt = contract.Now().Add(-sessionLife) }); err != nil {
		t.Fatal(err)
	}
	a.no("a session thirty days old", a.ask("GET", contract.APIv1+contract.APIView, "", nil), http.StatusForbidden, "not_paired")
	if err := contract.ChangePhones(b.p.Kit.Platform, func(list *contract.Phones) error { list.Paired = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	a.no("a forgotten app", a.ask("GET", contract.APIv1+contract.APIView, "", nil), http.StatusForbidden, "not_paired")
	access(t, b.p.Kit, false)
	nothing(t, "with phone access off", a.ask("GET", contract.RouteAPI, "", nil))
}

// The pairing code: good once, for a minute, and even then for no more than
// a number on the person's screen. A photograph of it gets nobody in.
func TestThePairingCode(t *testing.T) {
	b := house(t)
	a := &app{b: b, who: subject}
	a.no("with no code shown", a.present("word"), http.StatusForbidden, "wrong_code")
	a.no("after its minute", a.present(b.code(-time.Second)), http.StatusForbidden, "wrong_code")
	word := b.code(time.Minute)
	a.no("another word", a.present(rand.Text()), http.StatusForbidden, "wrong_code")
	a.no("no word", a.present(""), http.StatusForbidden, "wrong_code")
	for what, body := range map[string]string{"no JSON": "word=" + word, "no secret": `{"word":"` + word + `"}`, "a secret, not its hash": `{"word":"` + word + `","session":"` + rand.Text() + `"}`,
		"the hash of nothing": `{"word":"` + word + `","session":"` + contract.TokenHash("") + `"}`} {
		a.no(what, a.ask("POST", contract.APIv1+contract.APIPair, body, nil), http.StatusBadRequest, "bad_request")
	}
	if list := listOf(t, b); list.Asking != nil || list.Code == nil {
		t.Fatalf("a refused word asked or spent the code: %+v", list)
	}

	// Its first use spends it: nothing is paired, and the second use, by
	// whoever photographed it, is refused.
	number := numbered(t, a.present(word))
	thief := &app{b: b, who: kim}
	b.s.mu.Lock()
	b.s.pair.at = contract.Now().Add(-pairLife) // the first request has run out
	b.s.mu.Unlock()
	a.no("the code a second time", thief.present(word), http.StatusForbidden, "wrong_code")
	if list := listOf(t, b); len(list.Paired) != 0 || list.Code != nil {
		t.Fatalf("after one use: %+v", list)
	}
	a.no("without the approval", a.ask("GET", contract.APIv1+contract.APIView, "", nil), http.StatusForbidden, "not_paired")
	if b.decide(number, "approve"); len(listOf(t, b).Paired) != 0 {
		t.Fatal("a number that ran out was approved")
	}

	// A request is open: a second word waits its turn and is not spent.
	b.s.mu.Lock()
	b.s.refusals = 0
	b.s.mu.Unlock()
	first := numbered(t, a.present(b.code(time.Minute)))
	word = b.code(time.Minute)
	a.no("while another asks", thief.present(word), http.StatusTooManyRequests, "pairing_busy")
	if listOf(t, b).Code == nil {
		t.Error("a word was spent while another phone was asking")
	}
	// The thief's turn, within the minute: the person sees a number under a
	// name that is not theirs, refuses it, and the thief holds nothing.
	b.refused("refusing", b.decide(first, "refuse"), http.StatusSeeOther)
	number = numbered(t, thief.present(word))
	if asking := listOf(t, b).Asking; asking == nil || !strings.Contains(asking.Identity, kim) || asking.Name != "iPhone" {
		t.Fatalf("who the screens say is asking: %+v", asking)
	}
	b.refused("refusing", b.decide(number, "refuse"), http.StatusSeeOther)
	a.no("a refused app", thief.ask("GET", contract.APIv1+contract.APIView, "", nil), http.StatusForbidden, "not_paired")
	if list := listOf(t, b); len(list.Paired) != 0 || list.Asking != nil {
		t.Fatalf("after two refusals: %+v", list)
	}

	// Five refusals slow pairing, a good word included, as at the page.
	b.s.mu.Lock()
	b.s.refusals = 0
	b.s.mu.Unlock()
	for range slowFrom {
		a.no("a guess", a.present(rand.Text()), http.StatusForbidden, "wrong_code")
	}
	word = b.code(time.Minute)
	a.no("after five guesses", a.present(word), http.StatusTooManyRequests, "pairing_slow")
	if listOf(t, b).Code == nil {
		t.Error("a word was spent while pairing was slowed")
	}

	// The name an app gives itself is shown on the person's screen: letters,
	// digits and spaces, and few of them.
	b.s.mu.Lock()
	b.s.refusals = 0
	b.s.mu.Unlock()
	body, _ := json.Marshal(contract.AppPair{Word: word, Session: contract.TokenHash(rand.Text()), Name: "approve me\n<b>482 913</b> \x1b[2J"})
	if numbered(t, a.ask("POST", contract.APIv1+contract.APIPair, string(body), nil)); strings.ContainsAny(listOf(t, b).Asking.Name, "<>\n\x1b[/") {
		t.Errorf("the name of the app that asks: %q", listOf(t, b).Asking.Name)
	}
	body, _ = json.Marshal(contract.AppPair{Word: b.code(time.Minute), Session: contract.TokenHash(rand.Text()), Name: strings.Repeat("a long name ", 9)})
	b.s.mu.Lock()
	b.s.pair = pairing{}
	b.s.mu.Unlock()
	if numbered(t, a.ask("POST", contract.APIv1+contract.APIPair, string(body), nil)); listOf(t, b).Asking.Name != "an app" {
		t.Errorf("a name too long to be one: %q", listOf(t, b).Asking.Name)
	}
}

// Version 1's shapes are the contract's, held there by testdata/api1.json;
// here, that the door serves every route of that fixture and no other.
func TestEveryRouteOfVersionOneIsServed(t *testing.T) {
	b := house(t)
	a := b.installed(subject)
	s, _ := b.p.Record()
	b.s.k.Terms = screens{b.s.k.Terms, "> done"}
	b.s.k.PushKey = func() string { return "BK-the-public-half" }
	data, err := os.ReadFile("../contract/testdata/api1.json")
	var held struct {
		Routes []string
		Header string
	}
	if err != nil || json.Unmarshal(data, &held) != nil || len(held.Routes) != 11 || held.Header != contract.HeaderApp {
		t.Fatalf("the fixture: %v %+v", err, held)
	}
	fill := map[string]string{contract.APITask: s.Run.ID + "/T8", contract.APIChat: s.Run.ID + "/lead", contract.APIDo: "catchup"}
	for _, route := range held.Routes {
		path, method := route+fill[strings.TrimPrefix(route, contract.APIv1)], "GET"
		for _, post := range []string{contract.APIDo, contract.APITell, contract.APISet, contract.APIPair, contract.APIPush} {
			if route == contract.APIv1+post {
				method = "POST"
			}
		}
		if route == contract.APIv1+contract.APILive {
			continue // held open: the first test reads it
		}
		w := a.ask(method, path, `{"text":"hello","key":"theme","value":"kelp","endpoint":"https://push.example/x","p256dh":"k","auth":"a"}`, nil)
		if route == contract.APIv1+contract.APIPair {
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("a paired app pairing again: %d", w.Code)
			}
		} else if route == contract.APIv1+contract.APIPush {
			// Served, and it says what it waits for: nothing of the address is kept.
			a.no("an app's address for a nudge", w, http.StatusConflict, "waits_for_the_apps")
			if list, err := contract.ReadPhones(b.p.Kit.Platform.Read, b.s.dirs.State); err != nil || len(list.Paired) == 0 || list.Paired[0].Push != nil {
				t.Errorf("an app's push address was kept: %v %+v", err, list.Paired)
			}
		} else if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
			t.Errorf("%s %s: %d %.200s", method, path, w.Code, w.Body)
		}
	}
}
