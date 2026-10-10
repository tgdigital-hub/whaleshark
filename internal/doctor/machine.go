package doctor

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// inForce is what the SSH server must say of itself, whatever its files say.
var inForce = []string{"passwordauthentication no", "kbdinteractiveauthentication no", "permitrootlogin no", "allowtcpforwarding local",
	"allowstreamlocalforwarding local", "allowagentforwarding no", "gatewayports no", "x11forwarding no"}

// agentMemory is the memory one agent is taken to need, in megabytes, for
// the warning of the limits row; nobody has measured it on a server yet.
const agentMemory = 512

// shot is what the shared Node runs for the browser row: a picture of a
// page of its own through the sandbox, and 3 where a browser from the
// shared place runs with the sandbox switched off.
const shot = `const {chromium} = require('playwright'), ps = () => require('child_process').execSync('ps -eo args=').toString().split('\n');
(async () => { const b = await chromium.launch({chromiumSandbox: true}), p = await b.newPage();
  await p.goto('data:text/html,<h1>WhaleShark</h1>'); await p.screenshot({path: process.argv[1]});
  const off = ps().some(l => l.startsWith(process.env.PLAYWRIGHT_BROWSERS_PATH) && l.includes('--no-sandbox'));
  await b.close(); process.exit(off ? 3 : 0); })().catch(e => { console.error(String(e)); process.exit(1); });`

// hold says a row that passes or fails: ok with the first text, else a
// problem with the second and its fix.
func (e *exam) hold(pass bool, fix, good, bad string) {
	if pass {
		e.say(ok, "", "%s", good)
	} else {
		e.say(problem, fix, "%s", bad)
	}
}

// probe is doctor started by doctor as another login: it tries to open or
// to reach one port of this machine and prints how that went.
func probe(c *contract.Call) (any, error) {
	addr, word := net.JoinHostPort(contract.Loopback, c.Args[1]), "open"
	var end interface{ Close() error }
	var err error
	if c.Args[0] == "bind" {
		end, err = net.Listen("tcp", addr)
	} else {
		end, err = net.DialTimeout("tcp", addr, 2*time.Second)
	}
	if errors.Is(err, fs.ErrPermission) {
		word = "denied"
	} else if err != nil {
		word = "closed: " + err.Error()
	} else {
		end.Close()
	}
	fmt.Fprintln(c.Out, word)
	return word, nil
}

// machine is doctor --server, the proof of a server row by row: run by a
// work login it checks that login, run by root the machine and every block.
func (e *exam) machine() {
	p := e.k.Platform
	r, err := readRecord(p.Peek)
	switch {
	case p.System() != "linux":
		e.say(note, "", "--server checks a Linux server; this is %s", p.System())
		return
	case err != nil:
		e.say(problem, "", "the server's file cannot be used: %v", err)
		return
	case r == nil:
		e.say(note, setupCmd, "this machine is not set up as a server: it has no %s", contract.ServerFile)
		return
	}
	me := contract.Me()
	if day := restartNeeded(); day != "" {
		e.say(note, "sudo reboot", "restart needed since %s: an update waits for it, and the administrator picks the moment", day)
	}
	e.network()
	if _, listed := r.Login[me]; isRoot() {
		e.sshd()
		e.logins(r)
		e.blocks(r)
		e.road(r, r.names())
		e.say(note, "", "each work login runs whaleshark doctor --server itself for its own rows")
	} else if !listed {
		e.say(problem, addCmd+me, "%s is not a work login of this server; the machine's own rows are checked with sudo", me)
	} else {
		e.login(r, me)
		e.agent(r, me)
		e.browser()
		e.page()
		e.road(r, []string{me})
	}
}

// network: what listens beyond the machine itself, the automatic updates,
// which must never restart it, the one program file, and the firewall.
func (e *exam) network() {
	out, err := ask("ss", "-Hlnt")
	open := slices.DeleteFunc(strings.Split(out, "\n"), func(l string) bool {
		return l == "" || strings.Contains(l, " 127.") || strings.Contains(l, " [::1]:") || strings.Contains(l, ":22 ")
	})
	if err != nil || len(open) > 0 {
		e.say(note, "", "more than SSH may listen beyond this machine itself (%d in ss -lnt): only the firewall keeps that from outside", len(open))
	} else {
		e.say(ok, "", "nothing but SSH listens beyond this machine itself")
	}
	out, _ = ask("apt-config", "dump")
	e.hold(strings.Contains(out, `APT::Periodic::Unattended-Upgrade "1"`) && !strings.Contains(out, `Unattended-Upgrade::Automatic-Reboot "true"`), setupCmd,
		"automatic security updates are on, and none restarts the machine by itself", "automatic security updates are off, or one may restart the machine by itself under every running agent")
	self, _ := e.k.Platform.SelfPath()
	out, err = ask("stat", "-c", "%U %a", self)
	owner, mode, _ := strings.Cut(out, " ")
	perm, perr := strconv.ParseUint(mode, 8, 32)
	e.hold(err == nil && perr == nil && owner == "root" && perm&0o022 == 0, setupCmd, "the program file is root's and nobody else can write it: "+self,
		"the program file "+self+" must be root's and writable by nobody else; it is: "+out)
	out, err = ask("dpkg", "-S", self)
	if pkg, _, _ := strings.Cut(out, ":"); err != nil {
		e.say(note, "", "the program was not installed as a package, and no signed release exists yet to hold it against")
	} else {
		out, err = ask("dpkg", "--verify", pkg)
		e.hold(err == nil && out == "", "sudo apt-get install --reinstall "+pkg, "the package "+pkg+" is as its signed source delivered it", "the package "+pkg+" is not as it was installed: "+out)
	}
	if !isRoot() {
		return
	}
	// SSH's port is the one the SSH server names, as server harden reads it.
	conf, _ := ask("sshd", "-T")
	_, port, _ := strings.Cut("\n"+conf, "\nport ")
	port = strings.Fields(port + " 22")[0]
	out, _ = ask("ufw", "status", "verbose")
	var extra []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); strings.Contains(line, "ALLOW") && !slices.Contains([]string{port, port + "/tcp", "OpenSSH"}, f[0]) && !strings.Contains(line, "tailscale0") {
			extra = append(extra, f[0])
		}
	}
	e.hold(strings.Contains(out, "Status: active") && strings.Contains(out, "deny (incoming)") && len(extra) == 0, hardenCmd,
		"the firewall refuses everything from outside but SSH", "the firewall is off, or lets in more than SSH: "+cmp.Or(list(extra), "nothing is refused"))
}

// sshd holds the SSH server to the values in force, never to its files.
func (e *exam) sshd() {
	out, err := ask("sshd", "-T")
	wrong := slices.DeleteFunc(slices.Clone(inForce), func(want string) bool { return strings.Contains("\n"+out+"\n", "\n"+want+"\n") })
	e.hold(err == nil && len(wrong) == 0, hardenCmd, "SSH takes keys only and no root; it forwards to this machine alone and no agent",
		"the SSH server does not have in force: "+strings.Join(wrong, ", "))
}

// logins holds the two kinds of login apart: the administrator has sudo, a
// key and no agent; a work login has no sudo, one program, and services
// that outlive its sessions.
func (e *exam) logins(r *record) {
	sudo := func(name string) bool {
		out, err := ask("sudo", "-n", "-l", "-U", name)
		return err == nil && !strings.Contains(out, "not allowed")
	}
	home := func(name string) string {
		out, _ := ask("getent", "passwd", name)
		if f := strings.Split(out, ":"); len(f) > 5 {
			return sysRoot + f[5]
		}
		return ""
	}
	has := func(path ...string) bool { _, err := os.Stat(filepath.Join(path...)); return err == nil }
	at := home(r.Admin)
	keys, _ := os.ReadFile(filepath.Join(at, ".ssh", "authorized_keys"))
	switch {
	case at == "" || !sudo(r.Admin):
		e.say(problem, setupCmd, "the administrator login %q does not exist or has no sudo", r.Admin)
	case len(strings.TrimSpace(string(keys))) == 0:
		e.say(problem, setupCmd, "the administrator login %s has no SSH key: nobody can log in as it", r.Admin)
	case has(at, ".claude") || has(at, ".codex"):
		e.say(problem, "", "the administrator login %s has run an agent: it has sudo, so no agent belongs there", r.Admin)
	default:
		e.say(ok, "", "the administrator login %s has sudo and a key, and has never run an agent", r.Admin)
	}
	for _, name := range r.names() {
		linger, _ := ask("loginctl", "show-user", name, "--property=Linger", "--value")
		switch {
		case sudo(name):
			e.say(problem, "", "the work login %s has sudo: an agent there has the whole machine within reach", name)
		case has(home(name), ".local", "bin", "whaleshark"):
			e.say(problem, "", "%s has a program file of its own in .local/bin, which hides the machine's: delete it", name)
		case linger != "yes":
			e.say(problem, addCmd+name, "the services of %s end when it logs out: the engine is not kept alive without a session", name)
		default:
			e.say(ok, "", "the work login %s has no sudo, the machine's program, and services that outlive a session", name)
		}
	}
}

// blocks proves the two port rules for every work login, each tried as that
// login inside its own slice of the system: a listener of root's in its
// block is reached by it and by no other login, and it opens a port in its
// own block and in no other. Every login passing is what "proven" means.
func (e *exam) blocks(r *record) {
	self, _ := e.k.Platform.SelfPath()
	names, rules := r.names(), contract.RulesProven
	for i, name := range names {
		other, mine := "nobody", r.Login[name].Ports+contract.BlockSize-2
		if len(names) > 1 {
			other = names[(i+1)%len(names)]
		}
		theirs := contract.ServerPorts.Base + (mine+contract.BlockSize-contract.ServerPorts.Base)%contract.ServerPorts.Count
		l, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, strconv.Itoa(mine)))
		var found []string
		if err != nil {
			found = append(found, err.Error())
		}
		for j, t := range []struct {
			login, what string
			port        int
			open        bool
		}{{other, "dial", mine, false}, {name, "dial", mine, true}, {name, "bind", theirs, false}, {name, "bind", mine, true}} {
			if j == 2 && err == nil {
				l.Close()
			}
			uid, _ := ask("id", "-u", t.login)
			out, _ := ask("systemd-run", "--quiet", "--wait", "--pipe", "--collect", "--uid="+t.login, "--slice=user-"+uid+".slice", self, "doctor", "--server", t.what, strconv.Itoa(t.port))
			if shut := out == "denied" || t.what == "dial" && strings.HasPrefix(out, "closed"); t.open && out != "open" || !t.open && !shut {
				found = append(found, fmt.Sprintf("%s, %s %d: %s", t.login, t.what, t.port, out))
			}
		}
		if len(found) > 0 {
			rules = contract.RulesUnproven
		}
		e.hold(len(found) == 0, addCmd+name, "the port block of "+name+" is its alone: no other login opens a port there or reaches one",
			"the port block of "+name+" is not proven its alone. Went wrong: "+strings.Join(found, "; "))
	}
	if len(names) > 0 && r.Rules != rules {
		r.Rules = rules
		e.hold(r.write(e.k.Platform) == nil, "", "the server's file now says the port rules are "+rules, "the server's file could not be written to say the port rules are "+rules)
	}
}

// login is the work login's own rows: its folders and its temp folder its
// alone and on this machine's disk, what the server's file says of its
// block, and whether its processes are hidden, which nothing relies on.
func (e *exam) login(r *record, me string) {
	p := e.k.Platform
	home, _ := os.UserHomeDir()
	e.private("your home folder", home)
	e.private("your settings folder", e.dirs.Config)
	if kind, _ := ask("stat", "-f", "-c", "%T", e.dirs.State); slices.Contains([]string{"nfs", "cifs", "smb2", "ceph", "v9fs", "fuse"}, kind) {
		e.say(problem, "", "your state folder %s is on a network drive (%s), where the lock cannot be trusted", e.dirs.State, kind)
	}
	switch tmp := os.TempDir(); {
	case strings.HasPrefix(p.PathKey(tmp)+"/", p.PathKey(home)+"/"):
		e.say(ok, "", "temp files go to %s, inside your own folders", tmp)
	case os.Getenv(contract.EnvPane) != "":
		e.say(problem, restart, "this tab's temp folder is %s, which other logins share", tmp)
	default:
		e.say(note, "", "this shell's temp folder is %s, a shared one; a tab of the window has a private one, and doctor run there checks it", tmp)
	}
	block, _ := r.Block(me)
	if r.Rules == contract.RulesProven {
		e.say(ok, "", "your ports %d to %d are yours alone: both port rules were proven", block.Base, block.Base+block.Count)
	} else {
		e.say(note, proveCmd, "your ports %d to %d are not proven yours alone (the port rules are %s): until then another login may open or reach one", block.Base, block.Base+block.Count, cmp.Or(r.Rules, contract.RulesOff))
	}
	mounts, _ := os.ReadFile(sysRoot + "/proc/mounts")
	hidden := strings.Contains(string(mounts), "hidepid=2") || strings.Contains(string(mounts), "hidepid=invisible")
	e.say(note, "", "other logins' processes hidden from you: %v. That is a best effort nothing relies on; nothing secret of ours is ever on a command line", hidden)
}

// agent is what a work login needs before its agents run: the engine kept
// alive without a session, Claude signed in, git knowing who commits, and
// room under the cap.
func (e *exam) agent(r *record, me string) {
	home, _ := os.UserHomeDir()
	linger, _ := ask("loginctl", "show-user", me, "--property=Linger", "--value")
	unit, _ := ask("systemctl", "--user", "is-enabled", contract.UnitKeeper)
	e.hold(linger == "yes" && unit == "enabled", addCmd+me, "the engine is kept alive without a session",
		fmt.Sprintf("the engine is not kept alive without a session (lingering: %s; %s: %s): it ends when you log out", cmp.Or(linger, "?"), contract.UnitKeeper, cmp.Or(unit, "?")))
	if _, err := os.Stat(filepath.Join(home, ".claude", ".credentials.json")); err == nil {
		e.say(ok, "", "Claude is probably signed in: its credentials file is there, and no agent was started to ask")
	} else {
		e.say(note, "claude", "Claude does not look signed in: start it once and sign in")
	}
	name, _ := ask("git", "config", "--global", "user.name")
	email, _ := ask("git", "config", "--global", "user.email")
	e.hold(name != "" && email != "", `git config --global user.name "Your Name"; git config --global user.email you@example.com`,
		"git commits as "+name+" <"+email+">", "git does not know who commits here")
	if remote, _ := e.git("remote"); remote != "" {
		e.say(note, "", "whaleshark land --local merges into the checkout on this server and pushes nothing: with a remote, whaleshark land --pr is the usual way")
	}
	free, limit := 0, r.Cap(me)
	info, _ := os.ReadFile(sysRoot + "/proc/meminfo")
	if _, rest, found := strings.Cut(string(info), "MemAvailable:"); found {
		fmt.Sscan(rest, &free)
	}
	if free /= 1024; free < limit*agentMemory {
		e.say(note, "", "you may run up to %d agents, and %d MB of memory are free: the cap is a ceiling, not a promise that the machine carries it", limit, free)
	} else {
		e.say(ok, "", "you may run up to %d agents; %d MB of memory are free", limit, free)
	}
}

// browser takes one picture with the shared browser, sandbox on.
func (e *exam) browser() {
	dir, err := e.k.Platform.PrivateTemp()
	if _, missing := os.Stat(sysRoot + contract.ServerBrowsers); missing != nil || err != nil {
		e.say(note, setupCmd, "no shared browser is installed: a browser check reads \"not available on this server\"")
		return
	}
	defer os.RemoveAll(dir)
	out, err := ask("env", contract.EnvBrowsers+"="+contract.ServerBrowsers, "NODE_PATH="+contract.ServerNode+"/lib/node_modules",
		contract.ServerNode+"/bin/node", "-e", shot, filepath.Join(dir, "shot.png"))
	info, serr := os.Stat(filepath.Join(dir, "shot.png"))
	e.hold(err == nil && serr == nil && info.Size() > 0, setupCmd, "the shared browser took a picture of a test page, with its sandbox on",
		"the shared browser took no picture with its sandbox on: "+cmp.Or(out, "no file"))
}

// page signs in to the login's page as connect does and then asks it for a
// change from another site, which it must refuse.
func (e *exam) page() {
	self, _ := e.k.Platform.SelfPath()
	var answer struct{ Result contract.DashURL }
	if out, err := ask(self, "dash", "url", "--json"); err != nil || json.Unmarshal([]byte(out), &answer) != nil || answer.Result.Socket == "" {
		e.say(problem, "whaleshark dash start", "the page is not running")
		return
	}
	e.private("the page's socket", answer.Result.Socket)
	web := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return new(net.Dialer).DialContext(ctx, "unix", answer.Result.Socket)
		}}}
	at := "http://" + contract.PageHosts[0]
	in, err := web.Get(at + contract.RouteIn + "?" + contract.FieldCode + "=" + answer.Result.Code)
	if err != nil || in.Body.Close() != nil || in.StatusCode >= 400 || len(in.Cookies()) == 0 {
		e.say(problem, "whaleshark dash stop, then whaleshark dash start", "signing in to the page does not work")
		return
	}
	req, _ := http.NewRequest(http.MethodPost, at+contract.RouteDo+"stop", nil)
	req.Header.Set("Origin", "http://elsewhere.invalid")
	req.AddCookie(in.Cookies()[0])
	res, err := web.Do(req)
	e.hold(err == nil && res.Body.Close() == nil && res.StatusCode >= 400, "", "the page is running, signs in, and refuses a change asked for by another site",
		"the page did not refuse a change asked for by another site")
}

// road is the way in for a phone: Tailscale's forwarding to each login's
// door, and for a login itself what stands behind its door.
func (e *exam) road(r *record, names []string) {
	switch {
	case len(names) == 0:
		return
	case r.Road == "":
		e.say(note, phoneCmd+names[0], "no way in for a phone is set up on this server")
		return
	case r.Road != contract.RoadTailscale:
		e.say(note, "", "phones come in by %s: the checks of that way are %v", r.Road, contract.ErrNotBuilt)
		return
	}
	out, err := ask("tailscale", "serve", "status")
	for _, name := range names {
		door, port := net.JoinHostPort(contract.Loopback, strconv.Itoa(r.Door(name))), r.Login[name].Phone
		switch {
		case port == 0:
			e.say(note, phoneCmd+name, "no door for a phone is set up for %s", name)
		case err != nil:
			e.say(note, proveCmd, "Tailscale's forwarding could not be read here: %s", out)
		default:
			e.hold(strings.Contains(out, door) && (port == servePorts[0] || strings.Contains(out, ":"+strconv.Itoa(port))), phoneCmd+name, // the usual port is not printed
				fmt.Sprintf("Tailscale forwards port %d to the door of %s, %s", port, name, door), fmt.Sprintf("Tailscale does not forward port %d to the door of %s, %s", port, name, door))
		}
	}
	if phones, _ := contract.ReadPhones(e.k.Platform.Peek, e.dirs.State); isRoot() || !phones.On {
		return
	}
	door := net.JoinHostPort(contract.Loopback, strconv.Itoa(r.Door(names[0])))
	if conn, err := net.DialTimeout("tcp", door, time.Second); err != nil {
		e.say(problem, "whaleshark dash start", "phone access is on and nothing listens at your door %s", door)
	} else if conn.Close(); r.Rules != contract.RulesProven {
		e.say(note, proveCmd, "your door %s is open, and the port rules are not proven: pairing and the session are the only wall between another login and your page", door)
	} else {
		e.say(ok, "", "your door %s is open, and only you and the system reach it", door)
	}
}
