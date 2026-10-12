package doctor

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/install/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A server with a way in for phones: kim has a public name, ana
// has none yet.
var tunnelFile = ports(`version = 1
agents = 20
road = "cloudflare"
team = "myteam"
rules = "proven"
admin = "admin"

[login.kim]
ports = 31000
name = "ws1.example.com"
aud = "0a1b2c3d"
email = ["kim@example.com"]

[login.ana]
ports = 30000
`)

const (
	keysAt = "GET https://myteam.cloudflareaccess.com/cdn-cgi/access/certs"
	nameAt = "GET https://ws1.example.com/"
	refuse = "  - service: http_status:404\n"
)

// rule is one rule of the routing file as the script writes it.
func rule(name string, door int, check string) string {
	return fmt.Sprintf("  - hostname: %s\n    service: http://%s:%d\n%s", name, contract.Loopback, door, check)
}

// check is the connector's own check of Cloudflare's pass, in a rule.
func check(team, tag string) string {
	return "    originRequest:\n      access:\n        required: true\n        teamName: " + team + "\n        audTag: [" + tag + "]\n"
}

// tunnelled is a healthy server on the Cloudflare way: the connector as the
// script installs it, kim's rule in the routing file, the tunnel connected.
func tunnelled(t *testing.T, root bool) *box {
	b := server(t, tunnelFile, root)
	b.reach = map[int]bool{kimAt + contract.BlockSize - 1: true}
	b.routes(rule("ws1.example.com", kimAt+contract.BlockSize-1, check("myteam", "0a1b2c3d")) + refuse)
	for line, answer := range map[string]string{
		"cloudflared --version":                                                    "cloudflared version " + testkit.Connector + " (built for a test)",
		"cloudflared tunnel --config":                                              "Validating rules from the file\nOK",
		"apt-mark showhold cloudflared":                                            "cloudflared",
		"apt-cache policy cloudflared":                                             "cloudflared:\n  Installed: " + testkit.Connector + "\n     500 https://pkg.cloudflare.com/cloudflared any/main amd64 Packages",
		"systemctl show " + tunnelLogin + ".service":                               "User=" + tunnelLogin + "\nActiveState=active\nExecStart={ path=/usr/bin/cloudflared ; argv[]=/usr/bin/cloudflared tunnel --no-autoupdate --config /etc/whaleshark/cloudflared/config.yml run ; }",
		"stat -c %U %a " + b.sys + tunnelDir + "tunnel":                            tunnelLogin + " 400",
		"stat -c %U %a " + b.sys + tunnelDir + "config":                            "root 644",
		"GET http://" + net.JoinHostPort(contract.Loopback, tunnelPort) + "/ready": "200",
		keysAt: "200",
		nameAt: "302 https://myteam.cloudflareaccess.com/cdn-cgi/access/login/ws1.example.com?kid=0a1b2c3d",
	} {
		b.said[line] = answer
	}
	return b
}

func (b *box) routes(ingress string) {
	b.write(filepath.Join(b.sys, tunnelDir, "config.yml"), "tunnel: 0f0f\ncredentials-file: "+tunnelDir+"tunnel.json\ningress:\n"+ingress)
}

// asked is one request the gate was sent: the name it was asked under and
// what the pass it carried says, nil for none.
type asked struct {
	host   string
	claims map[string]any
}

// gate is a door of the test's own at kim's door port, with phone access
// on. It answers each request as answer says, and keeps what it was asked.
func (b *box) gate(answer func(w http.ResponseWriter, pass string)) (seen func() []asked) {
	b.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, strconv.Itoa(kimAt+contract.BlockSize-1)))
	if err != nil {
		b.Fatal(err)
	}
	var mu sync.Mutex
	var got []asked
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, pass := asked{host: r.Host}, r.Header.Get(contract.HeaderAccess)
		if parts := strings.Split(pass, "."); len(parts) == 3 {
			head, _ := base64.RawURLEncoding.DecodeString(parts[0])
			body, _ := base64.RawURLEncoding.DecodeString(parts[1])
			if json.Unmarshal(body, &a.claims) != nil || !strings.Contains(string(head), `"alg":"RS256"`) || len(parts[2]) < 300 {
				b.Errorf("a pass that is none: %s", pass)
			}
		}
		mu.Lock()
		got = append(got, a)
		mu.Unlock()
		answer(w, pass)
	})}
	go srv.Serve(l)
	b.Cleanup(func() { srv.Close() })
	b.phones()
	return func() []asked {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

func refuses(w http.ResponseWriter, _ string) { http.Error(w, "", http.StatusForbidden) }

// Root on a healthy server of the Cloudflare way passes the machine's rows,
// and the connector's login is tried at each door and beside it.
func TestRootOnAHealthyTunnel(t *testing.T) {
	b := tunnelled(t, true)
	e, exit := b.doctor("server")
	if exit != 0 || e.Problems != 0 {
		t.Fatalf("exit %d, %d problems:\n%s", exit, e.Problems, e.text())
	}
	for _, words := range [][]string{
		{"Cloudflare's connector is held at its version", "never updates itself", testkit.Connector}, {"tunnel's credential is readable by the connector's login alone"},
		{"no certificate of the Cloudflare account is on this machine"}, {"routing file is root's and valid"}, {"tunnel is connected to Cloudflare"},
		{"port block of kim is its alone"}, {"port block of ana is its alone"},
	} {
		b.want(e, ok, words...)
	}
	b.want(e, note, "no public name is set up for ana", phoneCmd+"ana")
	if e.line("older than nine months") != nil {
		t.Errorf("a line that does not belong here:\n%s", e.text())
	}
	var probes []string
	for _, line := range b.asked {
		if strings.HasPrefix(line, "systemd-run") && strings.Contains(line, "--uid="+tunnelLogin+" ") {
			probes = append(probes, line[strings.LastIndex(line, "dial"):])
		}
	}
	if strings.Join(probes, ",") != ports("dial 31998,dial 31999") {
		t.Errorf("the connector's login was tried at %v", probes)
	}
}

// Each fault of the machine on the Cloudflare way, seeded alone, is named.
func TestEachFaultOfTheTunnelIsNamed(t *testing.T) {
	door, kims := kimAt+contract.BlockSize-1, check("myteam", "0a1b2c3d")
	for _, c := range []struct {
		name  string
		seed  func(b *box)
		mark  string
		words []string
	}{
		{"not held", func(b *box) { b.said["apt-mark showhold cloudflared"] = "" }, problem, []string{"not held at one version", tunnelCmd + "myteam"}},
		{"another source", func(b *box) { b.said["apt-cache policy cloudflared"] = "500 http://archive.example.org/ubuntu" }, problem, []string{"from Cloudflare's own package source"}},
		{"another version", func(b *box) { script = "CONNECTOR='1999.1.0'\n" }, problem, []string{"not the version this release pins"}},
		{"as root", func(b *box) {
			b.said["systemctl show "+tunnelLogin+".service"] = strings.Replace(b.said["systemctl show "+tunnelLogin+".service"], "User="+tunnelLogin, "User=", 1)
		}, problem, []string{"does not run as its own login"}},
		{"stopped", func(b *box) {
			b.said["systemctl show "+tunnelLogin+".service"] = "User=" + tunnelLogin + "\nActiveState=failed"
		}, problem, []string{"does not run as its own login", "updater is not switched off"}},
		{"old", func(b *box) { b.said["cloudflared --version"] = "cloudflared version 2001.2.3 (built long ago)" }, note, []string{"older than nine months", "sudo whaleshark server upgrade"}},
		{"credential open", func(b *box) { b.said["stat -c %U %a "+b.sys+tunnelDir+"tunnel"] = tunnelLogin + " 644" }, problem, []string{"credential must be readable by " + tunnelLogin + " alone", "644"}},
		{"credential root's", func(b *box) { b.said["stat -c %U %a "+b.sys+tunnelDir+"tunnel"] = "root 400" }, problem, []string{"credential must be readable", "root 400"}},
		{"the account's certificate", func(b *box) { b.said["find "] = "/root/.cloudflared/cert.pem" }, problem, []string{"certificate of the Cloudflare account lies on this machine", "Delete it: /root/.cloudflared/cert.pem"}},
		{"routing file a login's", func(b *box) { b.said["stat -c %U %a "+b.sys+tunnelDir+"config"] = "kim 644" }, problem, []string{"routing file", "not root's alone to write (kim 644)"}},
		{"routing file writable", func(b *box) { b.said["stat -c %U %a "+b.sys+tunnelDir+"config"] = "root 664" }, problem, []string{"not root's alone to write"}},
		{"routing file refused", func(b *box) { b.said["cloudflared tunnel --config"] = "!Validation failed" }, problem, []string{"Cloudflare's own check refuses it"}},
		{"no routing file", func(b *box) { b.routes("") }, problem, []string{"last rule does not refuse everything else", "no rule of it leads ws1.example.com to the door of kim"}},
		{"no Access check", func(b *box) { b.routes(rule("ws1.example.com", door, "") + refuse) }, problem, []string{"ws1.example.com lacks the connector's check of Cloudflare's pass"}},
		{"the check not required", func(b *box) {
			b.routes(rule("ws1.example.com", door, strings.Replace(kims, "true", "false", 1)) + refuse)
		}, problem, []string{"ws1.example.com lacks the connector's check"}},
		{"another's tag", func(b *box) { b.routes(rule("ws1.example.com", door, check("myteam", "ffff0000")) + refuse) }, problem, []string{"ws1.example.com lacks the connector's check"}},
		{"another team", func(b *box) { b.routes(rule("ws1.example.com", door, check("theirs", "0a1b2c3d")) + refuse) }, problem, []string{"ws1.example.com lacks the connector's check"}},
		{"another port of the block", func(b *box) { b.routes(rule("ws1.example.com", door-1, kims) + refuse) }, problem, []string{"ws1.example.com leads to", ports(":31998 and not to the door of kim")}},
		{"a neighbour's door", func(b *box) { b.routes(rule("ws1.example.com", anaAt+contract.BlockSize-1, kims) + refuse) }, problem, []string{ports(":30999 and not to the door of kim")}},
		{"the SSH port", func(b *box) {
			b.routes(rule("ws1.example.com", door, kims) + rule("ssh.example.com", 22, kims) + refuse)
		}, problem, []string{"a rule for ssh.example.com, which is nobody's"}},
		{"a rule for every name", func(b *box) {
			b.routes(rule("ws1.example.com", door, kims) + "  - service: http://localhost:8080\n" + refuse)
		}, problem, []string{"a rule for every name, which is nobody's"}},
		{"nothing refused", func(b *box) { b.routes(rule("ws1.example.com", door, kims)) }, problem, []string{"last rule does not refuse everything else"}},
		{"a last rule that leads somewhere", func(b *box) { b.routes(rule("ws1.example.com", door, kims) + "  - service: http://localhost:8080\n") }, problem, []string{"last rule does not refuse everything else"}},
		{"not connected", func(b *box) { b.said["GET http://"+net.JoinHostPort(contract.Loopback, tunnelPort)+"/ready"] = "503" }, problem, []string{"tunnel is not connected to Cloudflare (503)", "sudo systemctl restart " + tunnelLogin}},
		{"connector silent", func(b *box) { b.said["GET http://"+net.JoinHostPort(contract.Loopback, tunnelPort)+"/ready"] = "" }, note, []string{"whether the tunnel is connected cannot be told"}},
		{"the connector beside the door", func(b *box) { b.reach[door-1] = true }, problem, []string{"port block of kim is not proven its alone", tunnelLogin + ports(", dial 31998: open")}},
		{"the connector kept from the door", func(b *box) { b.reach = nil }, problem, []string{"port block of kim is not proven", tunnelLogin + ports(", dial 31999: closed")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := tunnelled(t, true)
			c.seed(b)
			e, _ := b.doctor("server")
			b.want(e, c.mark, c.words...)
			if (c.mark == problem) != (e.Problems > 0) {
				t.Errorf("%d problems counted:\n%s", e.Problems, e.text())
			}
		})
	}
}

// A work login on the Cloudflare way: its own rule of the routing file, and
// its door tried with the four requests of somebody who has no pass, each
// under its public name. Nothing is asked beyond the machine without a yes.
func TestAWorkLoginBehindTheTunnel(t *testing.T) {
	b := tunnelled(t, false)
	seen := b.gate(refuses)
	e, exit := b.doctor("server")
	if exit != 0 || e.Problems != 0 {
		t.Fatalf("exit %d, %d problems:\n%s", exit, e.Problems, e.text())
	}
	b.want(e, ok, "routing file is root's and valid")
	b.want(e, ok, "tunnel is connected")
	b.want(e, ok, ports("31999 refuses a request with no pass of Cloudflare's, with a forged one"))
	b.want(e, note, "Cloudflare was not asked", "doctor asks first")
	if e.line("connector is held") != nil || e.line("ana") != nil {
		t.Errorf("a work login was shown the machine's or a neighbour's rows:\n%s", e.text())
	}
	got, now := seen(), float64(time.Now().Unix())
	if len(got) != 4 || got[0].claims != nil {
		t.Fatalf("the door was asked %d times: %v", len(got), got)
	}
	for i, a := range got {
		if a.host != "ws1.example.com" {
			t.Errorf("request %d asked under the name %q", i, a.host)
		}
		if i == 0 {
			continue
		}
		tag, until := fmt.Sprint(a.claims["aud"]), a.claims["exp"].(float64)
		if a.claims["iss"] != "https://myteam.cloudflareaccess.com" || a.claims["type"] != "app" || a.claims["email"] != "kim@example.com" ||
			(tag == "[0a1b2c3d]") != (i != 2) || (until > now) != (i != 3) {
			t.Errorf("pass %d says %v", i, a.claims)
		}
	}
	for _, line := range b.asked {
		if strings.HasPrefix(line, "GET https://") {
			t.Errorf("asked beyond this machine without a yes: %s", line)
		}
	}
}

// Phone access on with nothing at the door is a problem with its fix.
func TestADoorNobodyListensAtIsNamed(t *testing.T) {
	b := tunnelled(t, false)
	b.phones()
	e, _ := b.doctor("server")
	b.want(e, problem, "phone access is on and nothing listens at your door", "whaleshark dash start")
}

// What a door on the Cloudflare way must never do, and what the two
// requests out find once the person has said yes.
func TestEachFaultBehindTheTunnelIsNamed(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(w http.ResponseWriter, pass string)
		seed   func(b *box)
		typed  string
		mark   string
		words  []string
	}{
		{"a door that lets a forged pass in", func(w http.ResponseWriter, pass string) {
			if pass == "" {
				refuses(w, pass)
			}
		}, nil, "", problem, []string{ports("31999 did not refuse a pass signed with a key made here, such a pass for another tag, such a pass that has run out"), "whaleshark dash phone off"}},
		{"a door that lets everybody in", func(http.ResponseWriter, string) {}, nil, "", problem, []string{"did not refuse no pass, a pass signed"}},
		{"a door that sends to the pairing", func(w http.ResponseWriter, _ string) {
			w.Header().Set("Location", contract.RoutePair)
			w.WriteHeader(http.StatusSeeOther)
		}, nil, "", problem, []string{"did not refuse no pass"}},
		{"a door that refuses with a cookie", func(w http.ResponseWriter, pass string) {
			http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
			refuses(w, pass)
		}, nil, "", problem, []string{"did not refuse no pass"}},
		{"no values for the pass", refuses, func(b *box) {
			b.write(contract.ServerFile, strings.Replace(tunnelFile, `email = ["kim@example.com"]`, "", 1))
		}, "y\n", note, []string{"Cloudflare was not asked"}},
		{"a no", refuses, nil, "n\n", note, []string{"Cloudflare was not asked"}},
		{"a yes", refuses, nil, "yes\n", ok, []string{"https://ws1.example.com ends at Cloudflare's login and never at the page"}},
		{"keys out of reach", refuses, func(b *box) { b.said[keysAt] = "" }, "y\n", problem, []string{"Cloudflare's keys for the team myteam cannot be fetched", "refuses every phone"}},
		{"no such team", refuses, func(b *box) { b.said[keysAt] = "404" }, "y\n", problem, []string{"keys for the team myteam cannot be fetched (404"}},
		{"the name leads to the page", refuses, func(b *box) { b.said[nameAt] = "200" }, "y\n", problem, []string{"https://ws1.example.com did not end at Cloudflare's login (200", "whaleshark dash phone off"}},
		{"the name leads elsewhere", refuses, func(b *box) { b.said[nameAt] = "302 https://elsewhere.example.com/login" }, "y\n", problem, []string{"did not end at Cloudflare's login (302"}},
		{"the name answers nothing", refuses, func(b *box) { b.said[nameAt] = "" }, "y\n", problem, []string{"did not end at Cloudflare's login"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := tunnelled(t, false)
			if c.seed != nil {
				c.seed(b)
			}
			b.gate(c.answer)
			b.typed = c.typed
			e, _ := b.doctor("server")
			b.want(e, c.mark, c.words...)
			if (c.mark == problem) != (e.Problems > 0) {
				t.Errorf("%d problems counted:\n%s", e.Problems, e.text())
			}
			if c.name == "a yes" {
				b.want(e, ok, "Cloudflare's keys for the team myteam can be fetched")
			}
		})
	}
}
