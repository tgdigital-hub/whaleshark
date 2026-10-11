package doctor

import (
	"bufio"
	"cmp"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
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

// What the Cloudflare way has in fixed places, as the script writes them:
// the connector's login, which names its service too, its folder, and the
// port of this machine at which it says whether it is connected. pinned
// finds the connector's version in the script of a release.
const (
	tunnelLogin = "whaleshark-tunnel"
	tunnelDir   = "/etc/whaleshark/cloudflared/"
	tunnelPort  = "20241"
)

var pinned = regexp.MustCompile(`(?m)^CONNECTOR='(.+)'$`)

// get is one request that follows nowhere. host is the name asked for,
// where it is not the address's own, and pass a pass of Cloudflare's.
var get = func(url, host, pass string) (int, http.Header, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	if req.Host = host; pass != "" {
		req.Header.Set(contract.HeaderAccess, pass)
	}
	web := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// #nosec G107 G704 -- this login's door, the connector on this machine, or the team and the name the administrator wrote into the server's file, those two only after a yes
	res, err := web.Do(req)
	if err != nil {
		return 0, nil, err
	}
	return res.StatusCode, res.Header, res.Body.Close()
}

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
	out, _ = ask("git", "--version")
	e.hold(!cli.OldGit(out), "", "this system's git is new enough: "+out,
		"this system's git is older than git 2.38, the least WhaleShark works with: "+out+". A server is Ubuntu 24.04 or Debian 12, whose own git is new enough")
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
// On the Cloudflare way the connector's login reaches the door of a login
// with a public name, and no other port of its block.
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
		type try struct {
			login, what string
			port        int
			open        bool
		}
		tries := []try{{other, "dial", mine, false}, {name, "dial", mine, true}, {name, "bind", theirs, false}, {name, "bind", mine, true}}
		if r.Road == contract.RoadCloudflare && r.Login[name].Name != "" {
			// The door answers for the length of the proof, unless the page is at it already.
			if door, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, strconv.Itoa(mine+1))); err == nil {
				defer door.Close()
			}
			tries = append([]try{{tunnelLogin, "dial", mine, false}, {tunnelLogin, "dial", mine + 1, true}}, tries...)
		}
		for _, t := range tries {
			if t.what == "bind" && err == nil {
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
	// The same harmless button twice, with the session's own token from the
	// page: asked from the page's own address it is taken, from another
	// site's it is refused. Without the first, the second proves nothing.
	press := func(origin string) int {
		req, _ := http.NewRequest(http.MethodGet, at+contract.RouteTeam, nil)
		req.AddCookie(in.Cookies()[0])
		token := ""
		if res, err := web.Do(req); err == nil {
			text, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			_, token, _ = strings.Cut(string(text), `name="`+contract.FieldToken+`" value="`)
			token, _, _ = strings.Cut(token, `"`)
		}
		req, _ = http.NewRequest(http.MethodPost, at+contract.RouteDo+"catchup", strings.NewReader(contract.FieldToken+"="+token))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", origin)
		req.AddCookie(in.Cookies()[0])
		res, err := web.Do(req)
		if err != nil || res.Body.Close() != nil {
			return 0
		}
		return res.StatusCode
	}
	own, foreign := press(at), press("http://elsewhere.invalid")
	if own == 0 || own >= 400 {
		e.say(problem, "", "the page did not take a button pressed as the page itself presses it (%d), so what it refuses of another site was not proven", own)
		return
	}
	e.hold(foreign >= 400, "", "the page is running, signs in, and refuses a change asked for by another site", "the page did not refuse a change asked for by another site")
}

// road is the way in for a phone: Tailscale's forwarding to each login's
// door or Cloudflare's tunnel, and for a login itself what stands behind
// its door.
func (e *exam) road(r *record, names []string) {
	switch {
	case len(names) == 0:
		return
	case r.Road == "":
		e.say(note, phoneCmd+names[0], "no way in for a phone is set up on this server")
		return
	case r.Road == contract.RoadCloudflare:
		e.tunnel(r, names)
	default:
		e.forwarding(r, names)
	}
	if phones, _ := contract.ReadPhones(e.k.Platform.Peek, e.dirs.State); isRoot() || !phones.On {
		return
	}
	door := net.JoinHostPort(contract.Loopback, strconv.Itoa(r.Door(names[0])))
	if conn, err := net.DialTimeout("tcp", door, time.Second); err != nil {
		e.say(problem, "whaleshark dash start", "phone access is on and nothing listens at your door %s", door)
	} else if conn.Close(); r.Road == contract.RoadCloudflare {
		e.pass(r, names[0], door)
	} else if r.Rules != contract.RulesProven {
		e.say(note, proveCmd, "your door %s is open, and the port rules are not proven: pairing and the session are the only wall between another login and your page", door)
	} else {
		e.say(ok, "", "your door %s is open, and only you and the system reach it", door)
	}
}

func (e *exam) forwarding(r *record, names []string) {
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
}

// tunnel is the proof of the Cloudflare way: root's rows are the connector,
// its credential and that no certificate of the account is left; everybody's
// are the routing file and whether the tunnel is connected.
func (e *exam) tunnel(r *record, names []string) {
	fix := tunnelCmd + r.Team
	if isRoot() {
		version, _ := ask("cloudflared", "--version")
		held, _ := ask("apt-mark", "showhold", "cloudflared")
		from, _ := ask("apt-cache", "policy", "cloudflared")
		unit, _ := ask("systemctl", "show", tunnelLogin+".service", "-p", "User", "-p", "ActiveState", "-p", "ExecStart")
		var wrong []string
		miss := func(good bool, what string) {
			if !good {
				wrong = append(wrong, what)
			}
		}
		pin := pinned.FindStringSubmatch(script)
		miss(held == "cloudflared" && strings.Contains(from, "pkg.cloudflare.com"), "it is not held at one version from Cloudflare's own package source")
		miss(pin == nil || strings.Contains(version, "version "+pin[1]+" "), "it is not the version this release pins")
		miss(strings.Contains(unit, "User="+tunnelLogin) && strings.Contains(unit, "ActiveState=active"), "it does not run as its own login "+tunnelLogin)
		miss(strings.Contains(unit, "--no-autoupdate"), "its own updater is not switched off")
		e.hold(len(wrong) == 0, fix, "Cloudflare's connector is held at its version, runs as "+tunnelLogin+" and never updates itself: "+version, "Cloudflare's connector: "+strings.Join(wrong, "; "))
		// Its version is the year and the month it was made in.
		var year, month int
		_, made, _ := strings.Cut(version, "version ")
		if fmt.Sscanf(made, "%d.%d", &year, &month); year > 0 && e.c.Now.After(time.Date(year, time.Month(month+9), 1, 0, 0, 0, 0, time.UTC)) {
			e.say(note, "sudo whaleshark server upgrade", "Cloudflare's connector is older than nine months, and Cloudflare supports one for a year: a newer WhaleShark pins a newer one")
		}
		owner, _ := ask("stat", "-c", "%U %a", sysRoot+tunnelDir+"tunnel.json")
		e.hold(owner == tunnelLogin+" 400", fix, "the tunnel's credential is readable by the connector's login alone", "the tunnel's credential must be readable by "+tunnelLogin+" alone; it is: "+owner)
		left, _ := ask("find", sysRoot+"/", "(", "-path", sysRoot+"/proc", "-o", "-path", sysRoot+"/sys", "-o", "-path", sysRoot+"/dev", "-o", "-path", sysRoot+"/run", ")", "-prune",
			"-o", "-name", "cert.pem", "-path", "*cloudflare*", "-print")
		e.hold(left == "", "", "no certificate of the Cloudflare account is on this machine",
			"a certificate of the Cloudflare account lies on this machine, and whoever reads it makes tunnels and changes the account's names. Delete it: "+left)
	}
	e.routing(r, names, fix)
	switch status, _, err := get("http://"+net.JoinHostPort(contract.Loopback, tunnelPort)+"/ready", "", ""); {
	case err != nil:
		e.say(note, "", "whether the tunnel is connected cannot be told: the connector does not answer on this machine (%v)", err)
	case status != http.StatusOK:
		e.say(problem, "sudo systemctl restart "+tunnelLogin, "the connector says the tunnel is not connected to Cloudflare (%d)", status)
	default:
		e.say(ok, "", "the connector says the tunnel is connected to Cloudflare")
	}
}

// routing holds the routing file to the server's file: root's and valid,
// every rule a person's name that leads to that login's door behind the
// connector's own check of Cloudflare's pass for that person's tag, and a
// last rule that refuses all else.
func (e *exam) routing(r *record, names []string, fix string) {
	path, all := sysRoot+tunnelDir+"config.yml", r.names()
	owner, _ := ask("stat", "-c", "%U %a", path)
	_, invalid := ask("cloudflared", "tunnel", "--config", path, "ingress", "validate")
	text, _ := os.ReadFile(path)
	_, rules, _ := strings.Cut(string(text), "\ningress:\n")
	list := strings.Split("\n"+strings.TrimRight(rules, "\n"), "\n  - ")[1:]
	var wrong []string
	if owner != "root 644" {
		wrong = append(wrong, "it is not root's alone to write ("+owner+")")
	}
	if invalid != nil {
		wrong = append(wrong, "Cloudflare's own check refuses it")
	}
	if n := len(list) - 1; n < 0 || list[n] != "service: http_status:404" {
		wrong = append(wrong, "its last rule does not refuse everything else")
	} else {
		list = list[:n]
	}
	ruled := map[string]bool{}
	for _, rule := range list {
		field := func(key string) string {
			_, value, _ := strings.Cut(rule, key+": ")
			value, _, _ = strings.Cut(value, "\n")
			return value
		}
		host := field("hostname")
		i := slices.IndexFunc(all, func(n string) bool { return host != "" && r.Login[n].Name == host })
		switch {
		case i < 0:
			wrong = append(wrong, "a rule for "+cmp.Or(host, "every name")+", which is nobody's")
		case field("service") != "http://"+net.JoinHostPort(contract.Loopback, strconv.Itoa(r.Door(all[i]))):
			wrong = append(wrong, host+" leads to "+field("service")+" and not to the door of "+all[i])
		case field("required") != "true" || field("teamName") != r.Team || field("audTag") != "["+r.Login[all[i]].Aud+"]":
			wrong = append(wrong, host+" lacks the connector's check of Cloudflare's pass for its own tag")
		default:
			ruled[all[i]] = true
		}
	}
	for _, name := range names {
		if r.Login[name].Name == "" {
			e.say(note, phoneCmd+name, "no public name is set up for %s", name)
		} else if !ruled[name] {
			wrong = append(wrong, "no rule of it leads "+r.Login[name].Name+" to the door of "+name)
		}
	}
	e.hold(len(wrong) == 0, fix, "the tunnel's routing file is root's and valid: each name leads to its login's door behind the connector's check, and all else is refused",
		"the tunnel's routing file "+path+": "+strings.Join(wrong, "; "))
}

// pass tries the door as somebody without Cloudflare's pass would: with
// none, with one signed by a key made here, with such a one for another tag
// and with one that has run out. Then, once the person has said yes to a
// call out, it asks Cloudflare for the team's keys and for the public name
// as a stranger, who must end at Cloudflare's login.
func (e *exam) pass(r *record, me, door string) {
	a, known := r.Access(me)
	key, _ := rsa.GenerateKey(rand.Reader, 2048) // it fails for no size but one too small
	forged := func(tag string, until time.Time) string {
		b64, from := base64.RawURLEncoding.EncodeToString, until.Add(-time.Hour).Unix()
		claims, _ := json.Marshal(map[string]any{"iss": a.Issuer(), "aud": []string{tag}, "email": strings.Join(a.Email, " "), "sub": "doctor", "type": "app", "iat": from, "nbf": from, "exp": until.Unix()})
		text := b64([]byte(`{"alg":"RS256","kid":"doctor","typ":"JWT"}`)) + "." + b64(claims)
		sum := sha256.Sum256([]byte(text))
		signed, _ := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
		return text + "." + b64(signed)
	}
	var let []string
	for _, try := range [][2]string{{"no pass", ""}, {"a pass signed with a key made here", forged(a.Aud, e.c.Now.Add(time.Hour))},
		{"such a pass for another tag", forged("0", e.c.Now.Add(time.Hour))}, {"such a pass that has run out", forged(a.Aud, e.c.Now.Add(-time.Hour))}} {
		if status, head, err := get("http://"+door+"/", a.Name, try[1]); err == nil && (status < 400 || head.Get("Set-Cookie") != "") {
			let = append(let, try[0])
		}
	}
	e.hold(len(let) == 0, "whaleshark dash phone off", "your door "+door+" refuses a request with no pass of Cloudflare's, with a forged one, with one for another tag and with one run out",
		"your door "+door+" did not refuse "+strings.Join(let, ", ")+": close it until it does")
	answer := ""
	if known && !e.c.JSON && e.c.Stdin != nil {
		fmt.Fprintf(e.c.Out, "Ask Cloudflare for the keys of the team %s, and for https://%s as a stranger would? That is two requests out. [y/N] ", a.Team, a.Name)
		answer, _ = bufio.NewReader(e.c.Stdin).ReadString('\n')
	}
	if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
		e.say(note, "", "Cloudflare was not asked for its keys, nor your public name as a stranger would ask it: both call out, so doctor asks first, at a terminal")
		return
	}
	status, _, err := get(a.Issuer()+"/cdn-cgi/access/certs", "", "")
	e.hold(err == nil && status == http.StatusOK, "", "Cloudflare's keys for the team "+a.Team+" can be fetched",
		fmt.Sprintf("Cloudflare's keys for the team %s cannot be fetched (%d, %v): the page refuses every phone until they can", a.Team, status, err))
	status, head, err := get("https://"+a.Name+"/", "", "")
	e.hold(err == nil && status/100 == 3 && strings.HasPrefix(head.Get("Location"), a.Issuer()+"/"), "whaleshark dash phone off", "asked without a login, https://"+a.Name+" ends at Cloudflare's login and never at the page",
		fmt.Sprintf("asked without a login, https://%s did not end at Cloudflare's login (%d, %v): close the door until its Access application is in order", a.Name, status, err))
}
