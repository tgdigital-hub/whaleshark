package doctor

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/install"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The system as the server command and doctor --server meet it: a program
// of the system's run with a list of arguments, its fixed files under
// sysRoot, whether this is root, and the script that does the system's part
// of the server command. A test puts its own in the place of each.
var (
	sysRoot = ""
	isRoot  = func() bool { return os.Geteuid() == 0 }
	script  = install.Server
	ask     = func(name string, args ...string) (string, error) {
		// #nosec G204 -- a program this folder names, with a list of arguments and no shell
		out, err := exec.Command(name, args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
)

const (
	setupCmd  = "sudo whaleshark server setup --admin admin"
	proveCmd  = "sudo whaleshark doctor --server"
	addCmd    = "sudo whaleshark server add --user "
	phoneCmd  = "sudo whaleshark server phone --user "
	hardenCmd = "ssh <administrator>@<server> sudo whaleshark server harden"
)

// servePorts are the ports Tailscale's forwarding answers on: a person each.
var servePorts = []int{443, 8443, 10000}

// loginName is what a login of the system may be called here.
var loginName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// record is server.toml as its one writer keeps it: the contract's file,
// and the administrator login's name, for which the contract has no place.
type record struct {
	contract.Server
	Admin string `toml:"admin" json:"admin"`
}

// readRecord reads the server's file; nil and no error where there is none.
func readRecord(read func(string) ([]byte, error)) (*record, error) {
	s, err := contract.ReadServer(read)
	if s == nil {
		return nil, err
	}
	r := &record{Server: *s}
	data, _ := read(contract.ServerFile)
	return r, toml.Unmarshal(data, r)
}

// write puts the file in place whole, root's and readable by every login.
func (r *record) write(p contract.Platform) error {
	var text bytes.Buffer
	tmp := contract.ServerFile + ".new"
	// #nosec G301 -- every login reads the server's file; only root writes it and its folder
	err := errors.Join(toml.NewEncoder(&text).Encode(r), os.MkdirAll(filepath.Dir(tmp), 0o755))
	if err == nil {
		// #nosec G302 G306 -- as above: root's and readable by all by design, as start and doctor read it as any login
		err = errors.Join(os.WriteFile(tmp, text.Bytes(), 0o644), os.Chmod(tmp, 0o644))
	}
	if err != nil {
		return err
	}
	return p.Replace(tmp, contract.ServerFile)
}

func (r *record) names() []string { return slices.Sorted(maps.Keys(r.Login)) }

// restartNeeded is the day since which an update waits for a restart of the
// machine, from the mark the system leaves for it; empty when none does.
func restartNeeded() string {
	if info, err := os.Stat(sysRoot + "/var/run/reboot-required"); err == nil {
		return info.ModTime().Format(time.DateOnly)
	}
	return ""
}

// serve is the server command. It checks who calls and with what, decides
// what goes into the server's file, hands the system's part to the script
// compiled in, and writes the file only once that part is done. The script
// reads itself from standard input, finds this program's file in the
// variable contract.EnvBin, and is given: setup <admin> <key file or "">;
// harden <admin>; add <login> <first port> <key file or ""> <admin>;
// phone <login> <port outside> <door> <"" or "off">; upgrade; status.
func serve(c *contract.Call) (any, error) {
	p, verb := c.Kit.Platform, strings.Join(c.Args, " ")
	no := func(code, next, format string, a ...any) (any, error) {
		r := &contract.Refusal{Exit: contract.ExitEnv, Code: code, Message: fmt.Sprintf(format, a...)}
		if next != "" {
			r.Next = []string{next}
		}
		return nil, r
	}
	flag := func(name string) string { return strings.Join(c.Flags[name], "") }
	switch {
	case !slices.Contains(strings.Split(c.Command.Usage[len("server "):], "|"), verb):
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "Say which: whaleshark " + c.Command.Usage + ".", Next: []string{"whaleshark help server"}}
	case p.System() != "linux":
		return no("not_linux", "", "A server is a Linux machine (Ubuntu or Debian); this is %s.", p.System())
	case verb != "status" && !isRoot():
		return no("not_root", "sudo whaleshark server "+verb, "server %s changes the machine and needs the administrator.", verb)
	}
	r, err := readRecord(p.Read)
	if err != nil {
		return no("server_file", "", "The server's file cannot be used: %v.", err)
	} else if r == nil && verb != "setup" {
		return no("no_server", setupCmd, "This machine is not set up as a server: it has no %s.", contract.ServerFile)
	} else if r == nil {
		r = &record{Server: contract.Server{Versioned: contract.Versioned{Version: contract.FileVersion}, Agents: 20, Rules: contract.RulesOff}, Admin: "admin"}
	}
	user, args := flag("user"), []string{verb}
	person, listed := r.Login[user]
	if (verb == "add" || verb == "phone") && (!loginName.MatchString(user) || user == r.Admin) {
		return no("bad_login", "", "--user must name a work login, in small letters, and never the administrator's: %q.", user)
	}
	switch verb {
	case "setup":
		if r.Admin = cmp.Or(flag("admin"), r.Admin); !loginName.MatchString(r.Admin) || r.Login[r.Admin].Ports != 0 {
			return no("bad_login", "", "--admin must name a login that is nobody's work login, in small letters: %q.", r.Admin)
		}
		args = append(args, r.Admin, flag("admin-key"))
	case "harden":
		if os.Getenv("SUDO_USER") != r.Admin {
			return no("not_admin", "ssh "+r.Admin+"@<server> sudo whaleshark server harden",
				"server harden closes the old doors, so it runs only for someone who came in by the new one: as %s, over SSH, from your own computer.", r.Admin)
		}
		args = append(args, r.Admin)
	case "add":
		if free, room := r.Free(); !listed && !room {
			return no("full", "", "Every block of ports is taken: a server holds %d work logins.", contract.ServerPorts.Count/contract.BlockSize)
		} else if !listed {
			person.Ports, r.Rules = free, contract.RulesUnproven // a new block is nobody's until doctor --server has tried it
		}
		args = append(args, user, strconv.Itoa(person.Ports), flag("key"), r.Admin)
	case "phone":
		free := slices.IndexFunc(servePorts, func(port int) bool {
			return !slices.ContainsFunc(r.names(), func(n string) bool { return r.Login[n].Phone == port })
		})
		off := ""
		switch {
		case !listed:
			return no("no_login", addCmd+user, "%s is not a work login of this server.", user)
		case r.Road == contract.RoadCloudflare || flag("name")+flag("aud")+flag("email") != "":
			return no("not_built", "", "The Cloudflare way for a phone is %v.", contract.ErrNotBuilt)
		case c.Flags["off"] != nil:
			off = "off"
		case person.Phone == 0 && free < 0:
			return no("road_full", "", "Tailscale answers on %d ports only, so it carries %d people a machine; a further person needs the Cloudflare way.", len(servePorts), len(servePorts))
		case person.Phone == 0:
			person.Phone = servePorts[free]
		}
		args = append(args, user, strconv.Itoa(person.Phone), strconv.Itoa(r.Door(user)), off)
		if r.Road = contract.RoadTailscale; off != "" {
			person.Phone = 0
		}
	case "tunnel":
		return no("not_built", "", "server tunnel is %v.", contract.ErrNotBuilt)
	}
	self, _ := p.SelfPath()
	// #nosec G204 -- the system's shell reading the script compiled into this program; every argument was checked above
	sh := exec.Command("sh", append([]string{"-s", "--"}, args...)...)
	sh.Stdin, sh.Stdout, sh.Stderr, sh.Env = strings.NewReader(script), c.Out, c.Err, append(os.Environ(), contract.EnvBin+"="+self)
	if err := sh.Run(); err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitFailed, Code: "script", Message: fmt.Sprintf("server %s did not finish (%v). The server's file is as it was, and the command is safe to run again.", verb, err)}
	}
	if listed || verb == "add" {
		if r.Login == nil {
			r.Login = map[string]contract.Person{}
		}
		r.Login[user] = person
	}
	if !slices.ContainsFunc(r.names(), func(n string) bool { return r.Login[n].Phone != 0 }) && r.Road == contract.RoadTailscale {
		r.Road = ""
	}
	if slices.Contains([]string{"setup", "add", "phone"}, verb) {
		if err := r.write(p); err != nil {
			return no("server_file", "", "The server's file could not be written: %v.", err)
		}
	}
	return r, nil
}
