package doctor

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
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
)

// linux is the desk's platform saying it is a Linux server with the one
// program file in its place.
type linux struct {
	contract.Platform
	system string
}

func (l linux) System() string          { return l.system }
func (linux) SelfPath() (string, error) { return self, nil }

// short is the temp folder before any test moved it: a socket file's path
// must stay short.
var short = os.TempDir()

// box is a pretended server around a desk: the server's file and the
// system's fixed files in a temporary root, every program of the system
// answered from a table, and the two port rules kept by the box itself.
type box struct {
	*desk
	sys   string
	said  map[string]string // by how a command line starts; "!" first is a failure
	asked []string
	// bindRule and dialRule say whether the system keeps each port rule.
	bindRule, dialRule bool
	blocks             map[string]int
}

const (
	self  = "/usr/local/bin/whaleshark"
	kimAt = 31000
	anaAt = 30000
	// Every address of the machine, as the system prints it; written in two
	// halves, as nothing here holds a whole address in numbers.
	all4   = "0.0.0" + ".0"
	listen = "LISTEN 0 128 " + all4 + ":22 " + all4 + ":*\nLISTEN 0 128 [::]:22 [::]:*\nLISTEN 0 4096 127.0.0" + ".53%lo:53 " + all4 + ":*"
	file   = `version = 1
agents = 20
road = "tailscale"
rules = "proven"
admin = "admin"

[login.kim]
ports = 31000
phone = 443

[login.ana]
ports = 30000
`
)

// server builds a healthy server with the work login kim at the desk; root
// says whether doctor and the server command run as root.
func server(t *testing.T, text string, root bool) *box {
	t.Helper()
	d := project(t)
	if d.k.Platform.System() == "windows" {
		t.Skip("a pretended Linux server needs a host with its kind of paths and a sh")
	}
	b := &box{desk: d, sys: t.TempDir(), bindRule: true, dialRule: true, blocks: map[string]int{"kim": kimAt, "ana": anaAt}}
	d.k.Platform = linux{d.k.Platform, "linux"}
	me, wasRoot, wasAsk, wasRootDir, wasScript := contract.Me, isRoot, ask, sysRoot, script
	t.Cleanup(func() { contract.Me, isRoot, ask, sysRoot, script = me, wasRoot, wasAsk, wasRootDir, wasScript })
	contract.Me, isRoot, ask, sysRoot = func() string { return "kim" }, func() bool { return root }, b.ask, b.sys
	if root {
		contract.Me = func() string { return "root" }
	}
	if text != "" {
		d.write(contract.ServerFile, text)
	}
	home, _ := os.UserHomeDir()
	tmp := filepath.Join(d.dirs.Cache, "tmp")
	d.write(filepath.Join(tmp, "keep"), "")
	t.Setenv("TMPDIR", tmp)
	t.Setenv(contract.EnvPane, "")
	d.write(filepath.Join(home, ".claude", ".credentials.json"), "{}")
	d.write(filepath.Join(b.sys, "proc", "meminfo"), "MemTotal: 32000000 kB\nMemAvailable:   16000000 kB\n")
	d.write(filepath.Join(b.sys, "proc", "mounts"), "proc /proc proc rw,hidepid=2 0 0\n")
	d.write(filepath.Join(b.sys, "srv", "admin", ".ssh", "authorized_keys"), "ssh-ed25519 AAAA admin\n")
	b.said = map[string]string{
		"ss -Hlnt":                       listen,
		"apt-config dump":                "APT::Periodic::Unattended-Upgrade \"1\";\nUnattended-Upgrade::Automatic-Reboot \"false\";",
		"stat -c %U %a " + self:          "root 755",
		"stat -f":                        "ext2/ext3",
		"dpkg -S":                        "whaleshark: " + self,
		"dpkg --verify":                  "",
		"loginctl show-user":             "yes",
		"systemctl --user is-enabled":    "enabled",
		"git config --global user.name":  "Kim Example",
		"git config --global user.email": "kim@example.com",
		"tailscale serve status":         "https://box.example.ts.net (tailnet only)\n|-- / proxy http://" + contract.Loopback + ":31999",
		"sshd -T":                        "port 22\n" + strings.Join(inForce, "\n") + "\nmaxsessions 10",
		"ufw status verbose":             "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n\n22/tcp ALLOW IN Anywhere\n22/tcp (v6) ALLOW IN Anywhere (v6)",
		"sudo -n -l -U admin":            "User admin may run the following commands on box:\n    (ALL : ALL) NOPASSWD: ALL",
		"sudo -n -l -U":                  "!User is not allowed to run sudo on box.",
		"getent passwd admin":            "admin:x:1000:1000::/srv/admin:/bin/bash",
		"getent passwd kim":              "kim:x:1001:1001::/srv/kim:/bin/bash",
		"getent passwd ana":              "ana:x:1002:1002::/srv/ana:/bin/bash",
		"id -u":                          "1001",
		self + " dash url --json":        "!not running",
		"env " + contract.EnvBrowsers:    "",
	}
	b.write(filepath.Join(b.sys, contract.ServerBrowsers, "chromium"), "")
	b.page(true, true)
	return b
}

// ask answers for the system. A browser check leaves its picture unless it
// is told to fail; a probe started as another login gets the answer the two
// port rules give it, or the one the lack of a rule gives.
func (b *box) ask(name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	b.asked = append(b.asked, line)
	if name == "systemd-run" {
		login, port := strings.TrimPrefix(args[4], "--uid="), 0
		fmt.Sscan(args[len(args)-1], &port)
		own := port >= b.blocks[login] && port < b.blocks[login]+contract.BlockSize
		switch what := args[len(args)-2]; {
		case own || what == "bind" && !b.bindRule || what == "dial" && !b.dialRule:
			return "open", nil
		case what == "bind":
			return "denied", nil
		}
		return "closed: connection refused", nil
	}
	best, found := "", false
	for key := range b.said {
		if strings.HasPrefix(line, key) && len(key) >= len(best) {
			best, found = key, true
		}
	}
	out := b.said[best]
	if fail := strings.TrimPrefix(out, "!"); !found || fail != out {
		return fail, errors.New("exit status 1")
	}
	if name == "env" {
		b.write(args[len(args)-1], "a picture")
	}
	return out, nil
}

// page starts a page of the box's own on a socket file and makes dash url
// answer with it: it signs in with the code unless told not to, and refuses
// a change from another site unless told not to.
func (b *box) page(signsIn, refuses bool) string {
	b.Helper()
	dir, err := os.MkdirTemp(short, "ws")
	if err != nil {
		b.Fatal(err)
	}
	socket := filepath.Join(dir, "dash.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		b.Fatal(err)
	}
	os.Chmod(socket, 0o600)
	mux := http.NewServeMux()
	mux.HandleFunc(contract.RouteIn, func(w http.ResponseWriter, r *http.Request) {
		if !signsIn || r.URL.Query().Get(contract.FieldCode) != "c0de" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "one"})
		http.Redirect(w, r, contract.RouteTeam, http.StatusSeeOther)
	})
	mux.HandleFunc(contract.RouteDo, func(w http.ResponseWriter, r *http.Request) {
		if refuses && r.Header.Get("Origin") != "http://"+contract.PageHosts[0] {
			http.Error(w, "no", http.StatusForbidden)
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go srv.Serve(l)
	b.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	b.said[self+" dash url --json"] = fmt.Sprintf(`{"ok":true,"result":{"socket":%q,"code":"c0de","version":"dev"}}`, socket)
	return socket
}

// door listens where kim's phones come in and switches phone access on.
func (b *box) door() {
	b.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, strconv.Itoa(kimAt+contract.BlockSize-1)))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { l.Close() })
	b.phones()
}

func (b *box) phones() {
	b.Helper()
	if err := contract.ChangePhones(b.k.Platform, func(p *contract.Phones) error { p.On = true; return nil }); err != nil {
		b.Fatal(err)
	}
}

// A server set up as the design says passes every row of a work login.
func TestAWorkLoginOnAHealthyServer(t *testing.T) {
	b := server(t, file, false)
	b.door()
	e, exit := b.doctor("server")
	if exit != 0 || e.Problems != 0 {
		t.Fatalf("exit %d, %d problems:\n%s", exit, e.Problems, e.text())
	}
	for _, words := range [][]string{
		{"nothing but SSH listens"}, {"automatic security updates are on"}, {"program file is root's", self}, {"package whaleshark is as"},
		{"home folder", "yours alone"}, {"temp files go to", "inside your own folders"}, {"ports 31000 to 31999", "yours alone"},
		{"engine is kept alive without a session"}, {"Claude is probably signed in"}, {"git commits as Kim Example"},
		{"up to 20 agents; 15625 MB"}, {"shared browser took a picture"}, {"page's socket", "yours alone"}, {"page is running, signs in, and refuses"},
		{"Tailscale forwards port 443 to the door of kim"}, {"door", "31999 is open", "only you and the system"},
	} {
		b.want(e, ok, words...)
	}
	b.want(e, note, "processes hidden from you: true")
	if e.line("door of ana") != nil || e.line("SSH takes keys") != nil {
		t.Errorf("a work login was shown the machine's or a neighbour's rows:\n%s", e.text())
	}
}

// Each fault a work login can meet, seeded alone, is found and named with
// its mark and, where there is one, its fix.
func TestEachFaultOfAWorkLoginIsNamed(t *testing.T) {
	for _, c := range []struct {
		name  string
		file  string
		seed  func(b *box)
		mark  string
		words []string
	}{
		{"no server", "", nil, note, []string{"not set up as a server", setupCmd}},
		{"two logins one block", strings.Replace(file, "30000", "31000", 1), nil, problem, []string{"server's file cannot be used", "one block"}},
		{"a newer file", strings.Replace(file, "version = 1", "version = 9", 1), nil, problem, []string{"server's file cannot be used"}},
		{"not listed", strings.Replace(file, "login.kim", "login.eve", 1), nil, problem, []string{"kim is not a work login", addCmd + "kim"}},
		{"restart", file, func(b *box) { b.write(filepath.Join(b.sys, "var", "run", "reboot-required"), "") }, note, []string{"restart needed since " + time.Now().Format(time.DateOnly), "sudo reboot"}},
		{"listener", file, func(b *box) { b.said["ss -Hlnt"] = listen + "\nLISTEN 0 511 *:8080 *:*" }, note, []string{"more than SSH may listen", "(1 in"}},
		{"updates off", file, func(b *box) { b.said["apt-config dump"] = `APT::Periodic::Unattended-Upgrade "0";` }, problem, []string{"automatic security updates are off", setupCmd}},
		{"restarts itself", file, func(b *box) { b.said["apt-config dump"] += "\n" + `Unattended-Upgrade::Automatic-Reboot "true";` }, problem, []string{"restart the machine by itself"}},
		{"program writable", file, func(b *box) { b.said["stat -c %U %a "+self] = "root 775" }, problem, []string{"must be root's", "root 775"}},
		{"program a login's", file, func(b *box) { b.said["stat -c %U %a "+self] = "kim 755" }, problem, []string{"must be root's", "kim 755"}},
		{"package changed", file, func(b *box) { b.said["dpkg --verify"] = "??5?????? " + self }, problem, []string{"package whaleshark is not as it was installed", "--reinstall whaleshark"}},
		{"no package", file, func(b *box) { b.said["dpkg -S"] = "!no path found" }, note, []string{"not installed as a package"}},
		{"home open", file, func(b *box) { h, _ := os.UserHomeDir(); os.Chmod(h, 0o755) }, problem, []string{"your home folder", "open to another login", again}},
		{"network drive", file, func(b *box) { b.said["stat -f"] = "nfs" }, problem, []string{"state folder", "network drive (nfs)"}},
		{"shared temp in a tab", file, func(b *box) { b.Setenv("TMPDIR", b.sys); b.Setenv(contract.EnvPane, "p1") }, problem, []string{"this tab's temp folder", "other logins share"}},
		{"shared temp in a shell", file, func(b *box) { b.Setenv("TMPDIR", b.sys) }, note, []string{"this shell's temp folder", "a shared one"}},
		{"rules unproven", strings.Replace(file, "proven", "unproven", 1), nil, note, []string{"not proven yours alone", "port rules are unproven", proveCmd}},
		{"processes seen", file, func(b *box) { b.write(filepath.Join(b.sys, "proc", "mounts"), "proc /proc proc rw 0 0\n") }, note, []string{"processes hidden from you: false"}},
		{"no lingering", file, func(b *box) { b.said["loginctl show-user"] = "no" }, problem, []string{"not kept alive without a session", "lingering: no", addCmd + "kim"}},
		{"no keeper unit", file, func(b *box) { b.said["systemctl --user is-enabled"] = "!disabled" }, problem, []string{"not kept alive", contract.UnitKeeper + ": disabled"}},
		{"claude", file, func(b *box) {
			h, _ := os.UserHomeDir()
			os.Rename(filepath.Join(h, ".claude"), filepath.Join(h, "gone"))
		}, note, []string{"Claude does not look signed in"}},
		{"git", file, func(b *box) { b.said["git config --global user.email"] = "!" }, problem, []string{"git does not know who commits", "git config --global user.name"}},
		{"remote", file, func(b *box) { exec.Command("git", "-C", b.root, "remote", "add", "origin", "elsewhere").Run() }, note, []string{"land --local", "land --pr is the usual way"}},
		{"memory", file, func(b *box) { b.write(filepath.Join(b.sys, "proc", "meminfo"), "MemAvailable: 2048000 kB\n") }, note, []string{"up to 20 agents, and 2000 MB", "a ceiling"}},
		{"no browser", file, func(b *box) { os.Rename(filepath.Join(b.sys, "opt"), filepath.Join(b.sys, "gone")) }, note, []string{"no shared browser is installed", setupCmd}},
		{"browser fails", file, func(b *box) { b.said["env "+contract.EnvBrowsers] = "!No usable sandbox" }, problem, []string{"took no picture with its sandbox on", "No usable sandbox"}},
		{"page down", file, func(b *box) { b.said[self+" dash url --json"] = "!not running" }, problem, []string{"the page is not running", "whaleshark dash start"}},
		{"page socket open", file, func(b *box) { os.Chmod(b.page(true, true), 0o666) }, problem, []string{"the page's socket", "open to another login"}},
		{"no sign-in", file, func(b *box) { b.page(false, true) }, problem, []string{"signing in to the page does not work"}},
		{"foreign origin", file, func(b *box) { b.page(true, false) }, problem, []string{"did not refuse a change asked for by another site"}},
		{"no road", strings.Replace(file, `road = "tailscale"`, "", 1), nil, note, []string{"no way in for a phone", phoneCmd + "kim"}},
		{"cloudflare", strings.Replace(file, "tailscale", "cloudflare", 1), nil, note, []string{"come in by cloudflare", "not built yet"}},
		{"no door", strings.Replace(file, "phone = 443", "", 1), nil, note, []string{"no door for a phone is set up for kim", phoneCmd + "kim"}},
		{"no forwarding", file, func(b *box) { b.said["tailscale serve status"] = "No serve config" }, problem, []string{"does not forward port 443 to the door of kim", phoneCmd + "kim"}},
		{"forwarding unread", file, func(b *box) { b.said["tailscale serve status"] = "!access denied" }, note, []string{"forwarding could not be read", "access denied", proveCmd}},
		{"door shut", file, func(b *box) { b.phones() }, problem, []string{"phone access is on and nothing listens at your door", "whaleshark dash start"}},
		{"door open, rules unproven", strings.Replace(file, "proven", "unproven", 1), func(b *box) { b.door() }, note, []string{"31999 is open", "the only wall", proveCmd}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := server(t, c.file, false)
			if c.seed != nil {
				c.seed(b)
			}
			e, _ := b.doctor("server")
			b.want(e, c.mark, c.words...)
			if (c.mark == problem) != (e.Problems == 1) || c.mark != problem && e.Problems != 0 {
				t.Errorf("%d problems counted:\n%s", e.Problems, e.text())
			}
		})
	}
}

// Run as root, doctor checks the machine and proves every login's block; a
// file that said less is rewritten to say "proven", with all else kept.
func TestRootOnAHealthyServerProvesThePortRules(t *testing.T) {
	b := server(t, strings.Replace(file, "proven", "unproven", 1), true)
	e, exit := b.doctor("server")
	if exit != 0 {
		t.Fatalf("exit %d\n%s", exit, e.text())
	}
	for _, words := range [][]string{
		{"firewall refuses everything from outside but SSH"}, {"SSH takes keys only"}, {"administrator login admin has sudo and a key"},
		{"work login kim has no sudo"}, {"work login ana has no sudo"}, {"port block of kim is its alone"}, {"port block of ana is its alone"},
		{"server's file now says the port rules are proven"}, {"Tailscale forwards port 443 to the door of kim"},
	} {
		b.want(e, ok, words...)
	}
	b.want(e, note, "no door for a phone is set up for ana")
	b.want(e, note, "each work login runs")
	r, err := readRecord(os.ReadFile)
	if err != nil || r.Rules != contract.RulesProven || r.Admin != "admin" || r.Login["kim"].Phone != 443 || r.Login["ana"].Ports != anaAt || r.Road != contract.RoadTailscale {
		t.Errorf("the file after the proof: %+v %v", r, err)
	}
	probes := 0
	for _, line := range b.asked {
		if strings.HasPrefix(line, "systemd-run") {
			probes++
			if !strings.Contains(line, "--slice=user-1001.slice "+self+" doctor --server ") {
				t.Errorf("a probe not started in its login's slice: %s", line)
			}
		}
	}
	if probes != 8 {
		t.Errorf("%d probes for two logins, wanted four each", probes)
	}
}

// Each fault of the machine, seeded alone, is found and named; a port rule
// that does not hold takes "proven" out of the server's file again.
func TestEachFaultOfTheMachineIsNamed(t *testing.T) {
	for _, c := range []struct {
		name  string
		seed  func(b *box)
		rules string
		words []string
	}{
		{"firewall off", func(b *box) { b.said["ufw status verbose"] = "Status: inactive" }, "", []string{"firewall is off, or lets in more than SSH", "nothing is refused"}},
		{"firewall lets all in", func(b *box) {
			b.said["ufw status verbose"] = strings.Replace(b.said["ufw status verbose"], "deny (incoming)", "allow (incoming)", 1)
		}, "", []string{"firewall is off, or lets in more than SSH", hardenCmd}},
		{"firewall lets more in", func(b *box) { b.said["ufw status verbose"] += "\n8080/tcp ALLOW IN Anywhere" }, "", []string{"lets in more than SSH: 8080/tcp"}},
		{"passwords", func(b *box) {
			b.said["sshd -T"] = strings.Replace(b.said["sshd -T"], "passwordauthentication no", "passwordauthentication yes", 1)
		}, "", []string{"does not have in force: passwordauthentication no", hardenCmd}},
		{"agent forwarding and root", func(b *box) {
			b.said["sshd -T"] = strings.NewReplacer("allowagentforwarding no", "allowagentforwarding yes", "permitrootlogin no", "permitrootlogin without-password").Replace(b.said["sshd -T"])
		}, "", []string{"permitrootlogin no, allowagentforwarding no"}},
		{"sshd unread", func(b *box) { b.said["sshd -T"] = "!no hostkeys available" }, "", []string{"SSH server does not have in force"}},
		{"no administrator", func(b *box) { b.said["getent passwd admin"] = "!" }, "", []string{`administrator login "admin" does not exist or has no sudo`, setupCmd}},
		{"administrator without sudo", func(b *box) { b.said["sudo -n -l -U admin"] = "!User admin is not allowed to run sudo on box." }, "", []string{"does not exist or has no sudo"}},
		{"administrator without a key", func(b *box) { b.write(filepath.Join(b.sys, "srv", "admin", ".ssh", "authorized_keys"), "\n") }, "", []string{"admin has no SSH key"}},
		{"administrator ran an agent", func(b *box) { b.write(filepath.Join(b.sys, "srv", "admin", ".claude", "x"), "") }, "", []string{"admin has run an agent"}},
		{"work login with sudo", func(b *box) { b.said["sudo -n -l -U ana"] = "User ana may run the following commands" }, "", []string{"work login ana has sudo"}},
		{"a program of its own", func(b *box) { b.write(filepath.Join(b.sys, "srv", "kim", ".local", "bin", "whaleshark"), "") }, "", []string{"kim has a program file of its own"}},
		{"no lingering", func(b *box) { b.said["loginctl show-user ana"] = "no" }, "", []string{"services of ana end when it logs out", addCmd + "ana"}},
		{"no bind rule", func(b *box) { b.bindRule = false }, contract.RulesUnproven, []string{"port block of kim is not proven its alone", "kim, bind 21998: open", addCmd + "kim"}},
		{"no connect rule", func(b *box) { b.dialRule = false }, contract.RulesUnproven, []string{"port block of ana is not proven its alone", "kim, dial 30998: open"}},
		{"probes cannot run", func(b *box) { b.blocks = map[string]int{} }, contract.RulesUnproven, []string{"port block of kim is not proven", "kim, dial 31998: closed"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := server(t, file, true)
			c.seed(b)
			e, exit := b.doctor("server")
			b.want(e, problem, c.words...)
			if r, err := readRecord(os.ReadFile); exit != 1 || err != nil || r.Rules != cmp.Or(c.rules, contract.RulesProven) {
				t.Errorf("exit %d, the file says %v %v\n%s", exit, r, err, e.text())
			}
		})
	}
}

// The probe says what became of opening and of reaching a port.
func TestProbe(t *testing.T) {
	l, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, "0"))
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	try := func(what string) string {
		var out bytes.Buffer
		c := &contract.Call{Kit: contract.NewKit(), Flags: map[string][]string{"server": {""}}, Args: []string{what, port}, Out: &out}
		if _, err := run(c); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.String())
	}
	if got := try("dial"); got != "open" {
		t.Errorf("reaching a listener: %s", got)
	}
	if got := try("bind"); !strings.HasPrefix(got, "closed: ") {
		t.Errorf("opening a port that is taken: %s", got)
	}
	l.Close()
	if got := try("bind"); got != "open" {
		t.Errorf("opening a free port: %s", got)
	}
	if got := try("dial"); !strings.HasPrefix(got, "closed: ") {
		t.Errorf("reaching a port nobody listens at: %s", got)
	}
}

// serve runs the server command with flags given as name=value, and returns
// what the script was handed, a line a call.
func (b *box) serve(args ...string) (*record, *contract.Refusal, []string) {
	b.Helper()
	log := filepath.Join(b.sys, "script.log")
	b.Setenv("WS_LOG", log)
	script = "printf '%s,' \"$WHALESHARK_BIN\" \"$@\" >> \"$WS_LOG\"; echo >> \"$WS_LOG\"; [ \"$1\" != \"$WS_FAIL\" ]\n"
	c := &contract.Call{Kit: b.k, Command: contract.Find("server"), Flags: map[string][]string{}, Now: time.Now(), Out: new(bytes.Buffer), Err: new(bytes.Buffer)}
	for _, a := range args {
		if name, value, flag := strings.Cut(a, "="); flag {
			c.Flags[strings.TrimPrefix(name, "--")] = []string{value}
		} else {
			c.Args = append(c.Args, a)
		}
	}
	before, _ := os.ReadFile(log)
	result, err := serve(c)
	after, _ := os.ReadFile(log)
	var handed []string
	if text := strings.TrimPrefix(string(after), string(before)); text != "" {
		handed = strings.Split(strings.TrimSpace(text), "\n")
	}
	var r *contract.Refusal
	if errors.As(err, &r) {
		return nil, r, handed
	} else if err != nil {
		b.Fatal(err)
	}
	return result.(*record), nil, handed
}

// The server command from nothing: setup, two people, a phone each, and
// what the script is handed each time; the file is the contract's own.
func TestTheServerCommandWritesTheFileAndHandsTheScriptItsPart(t *testing.T) {
	b := server(t, "", true)
	step := func(want string, args ...string) *record {
		t.Helper()
		r, refusal, handed := b.serve(args...)
		if refusal != nil || len(handed) != 1 || handed[0] != self+","+want {
			t.Fatalf("server %v: %+v, the script got %q, wanted %q", args, refusal, handed, self+","+want)
		}
		if s, err := contract.ReadServer(os.ReadFile); slices.Contains([]string{"setup", "add", "phone"}, args[0]) && (err != nil || s == nil || s.Rules != r.Rules || len(s.Login) != len(r.Login)) {
			t.Fatalf("after server %v the contract reads %+v %v", args, s, err)
		}
		return r
	}
	if _, refusal, handed := b.serve("add", "--user=kim"); refusal == nil || refusal.Code != "no_server" || refusal.Next[0] != setupCmd || handed != nil {
		t.Fatalf("add before setup: %+v %v", refusal, handed)
	}
	if r := step("setup,boss,keys.pub,", "setup", "--admin=boss", "--admin-key=keys.pub"); r.Admin != "boss" || r.Version != contract.FileVersion || r.Agents != 20 || r.Rules != contract.RulesOff {
		t.Errorf("after setup: %+v", r)
	}
	if info, err := os.Stat(contract.ServerFile); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("the server's file is not readable by every login: %v %v", info, err)
	}
	if r := step("add,kim,21000,kim.pub,boss,", "add", "--user=kim", "--key=kim.pub"); r.Login["kim"].Ports != 21000 || r.Rules != contract.RulesUnproven {
		t.Errorf("after the first add: %+v", r)
	}
	if r := step("add,ana,22000,,boss,", "add", "--user=ana"); r.Login["ana"].Ports != 22000 || r.Door("ana") != 22999 {
		t.Errorf("after the second add: %+v", r)
	}
	r, _ := readRecord(os.ReadFile)
	r.Rules = contract.RulesProven
	if err := r.write(b.k.Platform); err != nil {
		t.Fatal(err)
	}
	if r := step("add,kim,21000,,boss,", "add", "--user=kim"); r.Rules != contract.RulesProven || len(r.Login) != 2 {
		t.Errorf("adding a login again changed what was proven: %+v", r)
	}
	if r := step("setup,boss,,", "setup"); r.Admin != "boss" || len(r.Login) != 2 {
		t.Errorf("setup run again lost something: %+v", r)
	}
	if r := step("phone,kim,443,21999,,", "phone", "--user=kim"); r.Road != contract.RoadTailscale || r.Login["kim"].Phone != 443 {
		t.Errorf("after the first phone: %+v", r)
	}
	if r := step("phone,ana,8443,22999,,", "phone", "--user=ana"); r.Login["ana"].Phone != 8443 {
		t.Errorf("after the second phone: %+v", r)
	}
	step("phone,kim,443,21999,,", "phone", "--user=kim")
	if r := step("phone,kim,443,21999,off,", "phone", "--user=kim", "--off="); r.Login["kim"].Phone != 0 || r.Road != contract.RoadTailscale {
		t.Errorf("after one phone was taken off: %+v", r)
	}
	if r := step("phone,ana,8443,22999,off,", "phone", "--user=ana", "--off="); r.Road != "" {
		t.Errorf("after the last phone was taken off: %+v", r)
	}
	step("upgrade,", "upgrade")
	b.Setenv("SUDO_USER", "boss")
	step("harden,boss,", "harden")
	b.write(filepath.Join(b.sys, "var", "run", "reboot-required"), "")
	isRoot = func() bool { return false }
	step("status,", "status")
}

// What the server command refuses, with the file left as it was and the
// script not started.
func TestTheServerCommandRefuses(t *testing.T) {
	full := "version = 1\nadmin = \"admin\"\nroad = \"tailscale\"\n"
	for i := 0; i < 11; i++ {
		full += fmt.Sprintf("[login.w%d]\nports = %d\nphone = %d\n", i, 21000+1000*i, append(servePorts, make([]int, 8)...)[i])
	}
	for _, c := range []struct {
		name, file string
		seed       func(b *box)
		args       []string
		code       string
		exit       int
	}{
		{"no verb", file, nil, nil, "usage", contract.ExitUsage},
		{"two words", file, nil, []string{"add", "kim"}, "usage", contract.ExitUsage},
		{"another system", file, func(b *box) { b.k.Platform = linux{b.k.Platform, "darwin"} }, []string{"status"}, "not_linux", contract.ExitEnv},
		{"not root", file, func(b *box) { isRoot = func() bool { return false } }, []string{"add", "--user=eve"}, "not_root", contract.ExitEnv},
		{"a newer file", strings.Replace(file, "version = 1", "version = 9", 1), nil, []string{"add", "--user=eve"}, "server_file", contract.ExitEnv},
		{"the administrator as a work login", file, nil, []string{"add", "--user=admin"}, "bad_login", contract.ExitEnv},
		{"a name that is none", file, nil, []string{"add", "--user=-x y"}, "bad_login", contract.ExitEnv},
		{"no name", file, nil, []string{"phone"}, "bad_login", contract.ExitEnv},
		{"a work login as the administrator", file, nil, []string{"setup", "--admin=kim"}, "bad_login", contract.ExitEnv},
		{"harden from the console", file, func(b *box) { b.Setenv("SUDO_USER", "") }, []string{"harden"}, "not_admin", contract.ExitEnv},
		{"harden as a work login", file, func(b *box) { b.Setenv("SUDO_USER", "kim") }, []string{"harden"}, "not_admin", contract.ExitEnv},
		{"a twelfth login", full, nil, []string{"add", "--user=eve"}, "full", contract.ExitEnv},
		{"a phone for nobody", file, nil, []string{"phone", "--user=eve"}, "no_login", contract.ExitEnv},
		{"a fourth phone", full, nil, []string{"phone", "--user=w5"}, "road_full", contract.ExitEnv},
		{"the other road", file, nil, []string{"phone", "--user=kim", "--name=ws1.example.com"}, "not_built", contract.ExitEnv},
		{"the tunnel", file, nil, []string{"tunnel"}, "not_built", contract.ExitEnv},
		{"the script fails", file, func(b *box) { b.Setenv("WS_FAIL", "add") }, []string{"add", "--user=eve"}, "script", contract.ExitFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := server(t, c.file, true)
			b.Setenv("WS_FAIL", "")
			if c.seed != nil {
				c.seed(b)
			}
			_, refusal, handed := b.serve(c.args...)
			if refusal == nil || refusal.Code != c.code || refusal.Exit != c.exit || (len(handed) > 0) != (c.code == "script") {
				t.Errorf("server %v: %+v, the script got %q", c.args, refusal, handed)
			}
			if now, _ := os.ReadFile(contract.ServerFile); string(now) != c.file {
				t.Errorf("the file changed:\n%s", now)
			}
		})
	}
}
