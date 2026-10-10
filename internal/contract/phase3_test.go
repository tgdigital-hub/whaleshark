package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// file answers ReadServer with a server's file, or with none.
func file(text string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		if path != ServerFile || text == "" {
			return nil, fs.ErrNotExist
		}
		return []byte(text), nil
	}
}

const twoPeople = `version = 1
agents = 12
road = "cloudflare"
team = "example-team"
rules = "unproven"

[login.ana]
ports = 22000
agents = 6

[login.kai]
ports = 21000
name = "ws1.example.org"
aud = "tag-of-kai"
email = ["kai@example.org"]
`

// A machine without the file is a person's own computer; with it every
// figure comes from the file: the block without its last port, which is the
// door, the cap, the next free block, and a person's values for Cloudflare,
// which count only when none is missing.
func TestServerFile(t *testing.T) {
	none, err := ReadServer(file(""))
	if block, ok := none.Block("anybody"); none != nil || err != nil || block != DefaultPorts || !ok || none.Door("anybody") != 0 || none.Cap("anybody") != 0 {
		t.Fatalf("no server: %v, %v, block %v %v", none, err, block, ok)
	}
	if _, ok := none.Access("anybody"); ok {
		t.Fatal("no server, and a person has values for Cloudflare")
	}
	s, err := ReadServer(file(twoPeople))
	if err != nil {
		t.Fatal(err)
	}
	kai, _ := s.Block("kai")
	ana, _ := s.Block("ana")
	if kai != (Ports{21000, 999}) || ana != (Ports{22000, 999}) || s.Door("kai") != 21999 || s.Door("ana") != 22999 {
		t.Errorf("blocks %v %v, doors %d %d", kai, ana, s.Door("kai"), s.Door("ana"))
	}
	if _, ok := s.Block("eve"); ok || s.Door("eve") != 0 {
		t.Error("a login the server does not list has a block or a door")
	}
	if s.Cap("kai") != 12 || s.Cap("ana") != 6 {
		t.Errorf("caps %d %d", s.Cap("kai"), s.Cap("ana"))
	}
	if next, ok := s.Free(); next != 23000 || !ok {
		t.Errorf("the next free block starts at %d, %v", next, ok)
	}
	// No slot of a task is ever the door, and no block holds a port of the
	// connector's own or of a person's own machine.
	if last := (Slot{N: 998, Size: 1}).First(kai); last >= s.Door("kai") || ServerPorts.holds(20245) || ServerPorts.holds(DefaultPorts.Base) {
		t.Errorf("the last slot's port is %d and the door %d", last, s.Door("kai"))
	}
	a, ok := s.Access("kai")
	if !ok || a.Issuer() != "https://example-team.cloudflareaccess.com" || a.Aud != "tag-of-kai" || a.Name != "ws1.example.org" || !slices.Equal(a.Email, []string{"kai@example.org"}) {
		t.Errorf("kai's values: %+v, %v", a, ok)
	}
	if _, ok := s.Access("ana"); ok {
		t.Error("ana has no name, tag or address, and her door would answer")
	}
	tailscale, _ := ReadServer(file(strings.Replace(twoPeople, "cloudflare", "tailscale", 1)))
	if _, ok := tailscale.Access("kai"); ok {
		t.Error("on the other road a person has values for Cloudflare")
	}
	full := &Server{Login: map[string]Person{}}
	for port := ServerPorts.Base; ServerPorts.holds(port); port += BlockSize {
		full.Login[strings.Repeat("u", port/BlockSize)] = Person{Ports: port}
	}
	if _, ok := full.Free(); ok || len(full.Login) != 11 {
		t.Errorf("a full server of %d logins has a free block", len(full.Login))
	}
}

// A server's file that cannot be taken as written is an error and never a
// machine with no server: a newer one, a road nobody knows, a block that
// starts between two blocks or outside the range, two logins on one block.
func TestServerFileRefused(t *testing.T) {
	for name, text := range map[string]string{
		"newer":       strings.Replace(twoPeople, "version = 1", "version = 2", 1),
		"road":        strings.Replace(twoPeople, "cloudflare", "carrier-pigeon", 1),
		"between":     strings.Replace(twoPeople, "22000", "22500", 1),
		"outside":     strings.Replace(twoPeople, "22000", "20000", 1),
		"shared":      strings.Replace(twoPeople, "22000", "21000", 1),
		"not a table": "login = 3\n",
	} {
		if s, err := ReadServer(file(text)); err == nil || s != nil {
			t.Errorf("%s: read as %+v", name, s)
		} else if name == "newer" && !errors.Is(err, ErrNewer) {
			t.Errorf("a newer file: %v", err)
		}
	}
}

// Who is calling at a phone's door is the road's word on Tailscale and
// never a header's on any other road.
func TestIdentity(t *testing.T) {
	header := func(name string) string {
		return map[string]string{HeaderTailscale: " kai@example.org ", HeaderAccess: "a.b.c"}[name]
	}
	if got := Identity(RoadTailscale, header); got != "kai@example.org" {
		t.Errorf("Tailscale named %q", got)
	}
	for _, road := range []string{RoadCloudflare, "", "other"} {
		if got := Identity(road, header); got != "" {
			t.Errorf("on road %q a header named %q", road, got)
		}
	}
}

// The phones have one writer, which twenty at once go through without
// losing one, and a change that is refused writes nothing.
func TestPhones(t *testing.T) {
	p := desk{dir: t.TempDir(), mu: new(sync.Mutex)}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			err := ChangePhones(p, func(list *Phones) error {
				list.On = true
				list.Paired = append(list.Paired, Phone{ID: "p" + string(rune('a'+i)), Session: TokenHash("secret")})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	refused := errors.New("no")
	if err := ChangePhones(p, func(list *Phones) error { list.Paired = nil; return refused }); err != refused {
		t.Fatal(err)
	}
	list, err := ReadPhones(p.Read, p.dir)
	if err != nil || !list.On || len(list.Paired) != 20 || list.Version != FileVersion {
		t.Fatalf("%d phones, on %v, version %d, %v", len(list.Paired), list.On, list.Version, err)
	}
	if strings.Contains(list.Paired[0].Session, "secret") {
		t.Error("the file holds a session's secret")
	}
}

// A project is listed once, however its path is written, and what was
// listed before stays.
func TestListProject(t *testing.T) {
	p := desk{dir: t.TempDir(), mu: new(sync.Mutex)}
	for _, root := range []string{"/a", "/b", "/a"} {
		if err := ListProject(p, root); err != nil {
			t.Fatal(err)
		}
	}
	if roots, err := ReadProjects(p.Read, p.dir); err != nil || !slices.Equal(roots, []string{"/a", "/b"}) {
		t.Fatalf("%v, %v", roots, err)
	}
}

// Until their packages are plugged in, a screen says that it is not built,
// no file of the page exists and no phone is reached; and the evidence
// stand-in opens nothing.
func TestThePageStandIns(t *testing.T) {
	k := NewKit()
	var b bytes.Buffer
	if err := k.Page(&b, &Page{Screen: ScreenTeam}); err != nil || !strings.Contains(b.String(), ErrNotBuilt.Error()) {
		t.Errorf("the page: %q, %v", b.String(), err)
	}
	if _, _, ok := k.Asset("page.css"); ok {
		t.Error("a file of a page that is not built")
	}
	if n, err := k.Push(Nudge{Title: "x"}, ""); n != 0 || err != nil || k.PushKey() != "" {
		t.Errorf("a nudge reached %d phones, %v", n, err)
	}
	if _, _, err := k.Evidence.Open("", "", "", 0); !errors.Is(err, ErrNotBuilt) || k.Evidence.Answering([]int{1}) != nil {
		t.Errorf("the evidence stand-in: %v", err)
	}
}

// Every route is its own, starts at the root, and none is the start of
// another but the team's, which is the root; a row of the action table is
// reached by its id after RouteDo. What a command prints with --json reads
// back into the types a screen is drawn from.
func TestRoutesAndEnvelope(t *testing.T) {
	routes := []string{RoutePlan, RouteMessages, RouteJobs, RouteTask, RouteLead, RouteIn, RouteDo, RouteLive,
		RouteEvidence, RouteSites, RouteAsset, RoutePair, RoutePush}
	for i, r := range routes {
		for j, other := range routes {
			if i != j && strings.HasPrefix(r, other) {
				t.Errorf("%s starts with %s", r, other)
			}
		}
		if !strings.HasPrefix(r, RouteTeam) {
			t.Errorf("%s does not start at the root", r)
		}
	}
	for _, a := range Actions {
		if strings.ContainsAny(a.ID, "/?# ") {
			t.Errorf("the action %q cannot follow %s", a.ID, RouteDo)
		}
	}
	printed, _ := json.Marshal(Envelope{OK: true, Result: []Job{{Root: "/a", View: &View{Run: "r3"}, Next: "whaleshark status"}}})
	var jobs []Job
	back := Envelope{Result: &jobs}
	if err := json.Unmarshal(printed, &back); err != nil || !back.OK || len(jobs) != 1 || jobs[0].View.Run != "r3" {
		t.Errorf("%s read back as %+v, %v", printed, jobs, err)
	}
}

// What a login's phones ask of the person is worded in one place, for every
// screen: the door's warning for an hour, then the phone that asks for two
// minutes, with the two buttons and, for a person, the two commands.
func TestWhatThePhonesAsk(t *testing.T) {
	at := time.Date(2026, 10, 8, 21, 14, 0, 0, time.UTC)
	p := Phones{Shut: at.Add(-59 * time.Minute), Asking: &Pairing{Number: "493 201", Name: "iPhone", Notes: []string{"its session will stand alone"}, At: at.Add(-time.Minute)}}
	got := p.Items(at, true)
	if len(got) != 2 || got[0].ID != "pass" || got[0].Buttons != nil || got[0].Next != nil || !strings.HasPrefix(got[0].Text, "Requests without a valid pass") {
		t.Fatalf("the items are %+v", got)
	}
	ask := got[1]
	if ask.ID != "pair493201" || ask.Text != "A phone wants to connect: 493 201 · nobody named · iPhone · its session will stand alone" || ask.News != ask.Text ||
		ask.Look != LookNeedsYou || ask.Holds || !ask.Since.Equal(p.Asking.At) ||
		!slices.Equal(ask.Next, []string{"whaleshark dash phone approve 493201", "whaleshark dash phone refuse 493201"}) {
		t.Errorf("the phone that asks is %+v", ask)
	}
	for i, b := range ask.Buttons {
		row := slices.IndexFunc(Actions, func(a Action) bool { return a.ID == b.Action })
		if want := []string{"dash", "phone", []string{"approve", "refuse"}[i], "{id}"}; row < 0 || !slices.Equal(Actions[row].Run, want) || b.Key != "yn"[i:i+1] {
			t.Errorf("button %d is %+v", i, b)
		}
	}
	// An agent is shown the item and told whose it is; nothing of it is an agent's to run.
	if agent := p.Items(at, false); len(agent) != 2 || !slices.Equal(agent[1].Next, []string{ForThePerson}) {
		t.Errorf("an agent is told %+v", agent)
	}
	p.Asking.Identity = "robin@example.com"
	if later := p.Items(at.Add(61*time.Second), true); len(later) != 0 {
		t.Errorf("after two minutes and after an hour: %+v", later)
	}
	if named := p.Items(at.Add(30*time.Second), true); len(named) != 2 || !strings.Contains(named[1].Text, " · robin@example.com · ") {
		t.Errorf("with the road naming somebody: %+v", named)
	}
	if none := (Phones{}).Items(at, true); none != nil {
		t.Errorf("a login with no phone asking: %+v", none)
	}
}
