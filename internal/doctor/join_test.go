package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/install"
	"github.com/tgdigital-hub/whaleshark/install/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// placed is a platform whose program file lies where the machine has it.
type placed struct {
	contract.Platform
	self string
}

func (p placed) SelfPath() (string, error) { return p.self, nil }

// The script compiled into the program, run by the real server command in a
// temporary root where a stand-in answers for every program of the system:
// the two agree on every word handed over, the file follows the script, and
// doctor --server finds what the script set up where the script put it.
func TestTheServerCommandRunsTheScriptCompiledIn(t *testing.T) {
	system := ask
	b := server(t, "", true)
	m := testkit.New(t, install.Server)
	for _, pair := range m.Env {
		if name, value, _ := strings.Cut(pair, "="); name != "HOME" {
			t.Setenv(name, value)
		}
	}
	b.k.Platform = placed{b.k.Platform, m.Self}
	ask, sysRoot, script = system, m.Root, install.Server
	run := func(args ...string) (*record, string, error) {
		t.Helper()
		c := &contract.Call{Kit: b.k, Command: contract.Find("server"), Flags: map[string][]string{}, Now: time.Now(), Out: new(bytes.Buffer), Err: new(bytes.Buffer)}
		for _, a := range args {
			if name, value, flag := strings.Cut(a, "="); flag {
				c.Flags[strings.TrimPrefix(name, "--")] = []string{value}
			} else {
				c.Args = append(c.Args, a)
			}
		}
		result, err := serve(c)
		said := c.Out.(*bytes.Buffer).String() + c.Err.(*bytes.Buffer).String()
		if err != nil {
			return nil, said, err
		}
		return result.(*record), said, nil
	}
	must := func(args ...string) (*record, string) {
		t.Helper()
		r, said, err := run(args...)
		if err != nil {
			t.Fatalf("server %v: %v\n%s", args, err, said)
		}
		return r, said
	}
	read := func(path ...string) string {
		data, _ := os.ReadFile(filepath.Join(path...))
		return string(data)
	}
	asked := func(call string) {
		t.Helper()
		if log := read(m.Fake, "log"); !strings.Contains(log, call) {
			t.Errorf("the system was never asked for %q:\n%s", call, log)
		}
	}

	if r, _ := must("setup", "--admin=boss"); r.Admin != "boss" {
		t.Errorf("after setup: %+v", r)
	}
	if got := read(m.Root, "usr", "local", "bin", "whaleshark"); got != read(m.Self) {
		t.Errorf("the program in place is %q", got)
	}
	if read(m.Root+contract.ServerBrowsers, ".pin") == "" {
		t.Errorf("no browser where doctor and a tab look for it, %s", contract.ServerBrowsers)
	}
	t.Setenv("SUDO_USER", "boss")
	must("harden")
	asked("systemctl reload ssh")

	// A key file's name goes to the script as typed, spaces and all.
	keys := filepath.Join(t.TempDir(), "my keys", "ben.pub")
	b.write(keys, read(m.Root, "people", "first", ".ssh", "authorized_keys"))
	must("add", "--user=ana")
	r, _ := must("add", "--user=ben", "--key="+keys)
	if r.Login["ana"].Ports != 21000 || r.Login["ben"].Ports != 22000 || r.Rules != contract.RulesUnproven {
		t.Errorf("after two logins: %+v", r)
	}
	if got := read(m.Root, "people", "ben", ".ssh", "authorized_keys"); got == "" || got != read(keys) {
		t.Errorf("ben is let in by %q", got)
	}
	// The block the file gives a login is the block the system holds it to;
	// the pretended machine numbers its logins from 1000, these the fourth on.
	for i, name := range []string{"ana", "ben"} {
		first := strconv.Itoa(r.Login[name].Ports)
		slice := read(m.Root, "etc", "systemd", "system", "user-"+strconv.Itoa(1003+i)+".slice.d", "50-whaleshark.conf")
		if !strings.HasPrefix(slice, "# "+name+" ports "+first+"\n") || !strings.Contains(slice, "SocketBindAllow="+first+"-"+strconv.Itoa(r.Door(name))+"\n") {
			t.Errorf("the share of %s is\n%s", name, slice)
		}
		asked("systemctl --user -M " + name + "@ enable -q --now " + contract.UnitKeeper)
	}
	door := "http://" + contract.Loopback + ":" + strconv.Itoa(r.Door("ana"))
	if r, _ = must("phone", "--user=ana"); r.Login["ana"].Phone != servePorts[0] || r.Road != contract.RoadTailscale {
		t.Errorf("after phone: %+v", r)
	}
	asked("tailscale serve --bg --https=" + strconv.Itoa(servePorts[0]) + " " + door)

	// doctor --server, asking the same stand-ins, finds the script's work.
	e, _ := b.doctor("server")
	for _, words := range [][]string{{"SSH takes keys only"}, {"firewall refuses everything from outside but SSH"},
		{"Tailscale forwards port", "to the door of ana"}, {"no door for a phone is set up for ben"}} {
		if l := e.line(words...); l == nil || l.Mark == problem {
			t.Errorf("doctor --server does not find %q as the script left it:\n%s", words, e.text())
		}
	}

	// One line says a restart waits, and it is the script's.
	b.write(filepath.Join(m.Root, "var", "run", "reboot-required"), "")
	if _, said := must("status"); !strings.Contains(said, "ana  ports 21000-21999  keeper active  page active") || strings.Count(said, "restart needed since") != 1 {
		t.Errorf("status said:\n%s", said)
	}
	// A script that fails leaves the file as it was.
	before := read(contract.ServerFile)
	if _, said, err := run("add", "--user=cy", "--key="+filepath.Join(m.Root, "none")); err == nil || !strings.Contains(said, "not a file of public keys") || read(contract.ServerFile) != before {
		t.Errorf("a login whose key file is none: %v\n%s", err, said)
	}
	b.write(m.Self, "the program, version two\n")
	must("upgrade")
	if got := read(m.Root, "usr", "local", "bin", "whaleshark"); got != "the program, version two\n" {
		t.Errorf("after the upgrade the program in place is %q", got)
	}
	asked("systemctl --user -M ben@ restart " + contract.UnitDash)
	if r, _ = must("phone", "--user=ana", "--off="); r.Login["ana"].Phone != 0 || r.Road != "" {
		t.Errorf("after the phone was taken off: %+v", r)
	}
	asked("tailscale serve --https=" + strconv.Itoa(servePorts[0]) + " off")
}
