# WhaleShark on Linux: what is proven, and the list for the first real servers

Two halves. The first runs by itself in containers and needs no server. The second needs three rented servers, a private network, a Cloudflare account with a domain, and a phone; it is written here as the script for that day and **has not been run**.

## 1. The half that runs in containers

`test/platform/linux/` holds it: one Go test that starts the containers, one script that runs inside each (`testdata/inside.sh`), and two checks a work login makes there.

| Container (its maker's official image) | Run as |
|---|---|
| `alpine:3.22` | a plain machine |
| `debian:12`, `ubuntu:22.04`, `ubuntu:24.04` | a plain machine, and a server |

**Where it runs.** On a hosted Linux machine of the pipeline, as part of `go test ./...`: the plain part on every push, the server part on a branch whose name ends in `linux`, because it fetches a browser, Node and Claude Code three times over and takes minutes. Anywhere else with Linux, Go and a running Docker:

```
WHALESHARK_CONTAINERS=plain  go test -count=1 -v -run TestContainers ./test/platform/linux/
WHALESHARK_CONTAINERS=server go test -count=1 -v -timeout 30m -run TestContainers ./test/platform/linux/
```

A container is handed this tree, the machine's own Go and modules and the program built as the README builds it, all to read only. It installs git from the system's own packages, makes a login `robin`, and works as that login.

### What a plain container proves

- The program file starts and answers on each system (`version`, `help`), built once, with no C library of its own.
- With each system's own git: the tests of the overlap scan, of the copies of the code and of collecting work, and the scenarios of phase 2, which include two tasks changing one file.
- A screenshot counts from the second its attempt began: a picture given the very instant of the start is taken by `report done`, and one given the instant before that second is refused. So the file system keeps a file's time at least to the second and never earlier than it was.

### What a container set up as a server proves

The real `whaleshark server` command with the real script, as root, against the system's real package manager and the makers' real package sources. Nothing of the system is pretended except what the next table names.

| Step | Proven on Debian 12, Ubuntu 22.04 and 24.04 |
|---|---|
| `server setup --admin admin` | ends well from nothing: the system's packages, the administrator login with the caller's key and `sudo` without a password, automatic updates without restart, the program in `/usr/local/bin`, Claude Code from its maker's source after the fingerprint matched, Node at the pinned checksum, the shared browser with its system packages |
| the same again | changes nothing |
| what it left | the program root's and mode 755; the `sudo` line 440; the server's file root's and 644; everything under `/opt/whaleshark` root's and written by nobody else |
| `server add --user robin`, twice | the login, its home closed to others, its block of ports in the server's file, in its slice's file and in the rule file; the second time nothing changes |
| who may connect | with the rule file loaded by the real `nft`: a listener of `robin`'s in its block is reached by `robin` and by root, and refused to another login |
| `init` | refused with `outside_home` in a folder outside the login's home; not so inside it |
| `server upgrade` | a newer program file replaces the one in `/usr/local/bin`, root's and 755 |
| the worker's screenshot command | the prompt's own line for a server, read from a real prompt and run as written by the work login, saves a picture of 1280 by 900 with its profile in the attempt's own folder, and `report done` takes it |
| `doctor --server` | runs as root and as the work login; its rows are printed, not judged (see below) |

### What a container cannot prove

| Not proven | Why |
|---|---|
| the slices: who may open a port in a block, a login's share of memory, processor and tasks | a container has no system manager. `systemctl` and `loginctl` are stand-ins that answer yes and do nothing, so the files are written and never put in force |
| the keeper and the page as a login's services, lingering, the restart of each page by `server upgrade` | the same |
| `server harden`: SSH's settings in force, the firewall, the reserved range of ports | it runs only for someone who came in over SSH as the administrator, and reloads a service |
| the browser's guard on Ubuntu 24.04 | a container may load no rule into the kernel's guard of programs; `apparmor_parser` is a stand-in |
| hiding other logins' processes | a container may not mount `/proc` again; setup says so and goes on, as designed |
| `doctor --server` as a whole | most of its rows ask the system manager, so they read "problem" here whatever the truth |
| `install.sh` | there is no release for it to fetch and no signer in it yet; the program is copied to where it would leave it |
| anything of a phone, of Tailscale and of Cloudflare | they need the real things |

## 2. The day there are servers: the script

Not run. Each step says what must be seen. Do all of it on Ubuntu 24.04 first, then parts A and B again on Ubuntu 22.04 and on Debian 12. `robin` and `sam` are two work logins, `admin` the administrator; `$LO` stands for the IPv4 loopback address.

**Waiting for the owner's yes before this can begin:** a draft release with its signed list and its apt repository (the install steps fetch from it); the connector's version and the checksum of Cloudflare's package key written into the server script; the three servers, the private network, the Cloudflare account with a domain, an iPhone and an Android phone.

### A. From nothing, twice over

The whole of A is done once with `install.sh` and once, on a second fresh machine of the same system, with the three lines of apt from the draft's repository in the place of steps 1 and 2.

| # | Where | Command | Must be seen |
|---|---|---|---|
| 1 | server | `curl -fsSLO <draft>/install.sh`, then `sh install.sh` | the signature checked, the key's fingerprint printed and equal to the README's, the program in `~/.local/bin` |
| 2 | server | the same with one byte of the fetched program changed | refused, nothing installed |
| 3 | server | `sudo ~/.local/bin/whaleshark server setup --admin admin` | ends well; watch which hosts it speaks to: the system's package source, Claude Code's, nodejs.org, the browser tooling's registry and download host, and no other |
| 4 | own computer | `ssh admin@<server> sudo whaleshark server harden` | the old doors closed, this session still open |
| 5 | provider's console | `sudo whaleshark server harden` | refused, nothing changed |
| 6 | own computer | `ssh admin@<server> sudo whaleshark server add --user robin`, then `--user sam --key sam.pub` | two blocks of ports, the keeper and the page of each switched on |
| 7 | server | each of 3, 4 and 6 again | nothing changes |
| 8 | server | `sudo whaleshark doctor --server`, then as `robin` and as `sam` | no problem row. Read each row against the machine by hand once; first the rows the stand-ins could not answer: updates, the administrator's key, `sudo`, the port proof |
| 9 | server | `ls -l` of `/etc/whaleshark`, `/etc/sudoers.d/whaleshark-admin`, `/usr/local/bin/whaleshark`, `/opt/whaleshark`, each home; as `robin`, try to write each | root's, nobody else writes, homes 700; with root's `umask` at 077 every login still reads the server's file |
| 10 | server | `sudo reboot`, then `ssh robin@<server> whaleshark engine status` | the keeper running before any session of `robin`'s |
| 11 | server | a newer draft installed the same way, `sudo whaleshark server upgrade` while `sam` has a run open; `whaleshark wait` in each login | one `upgraded` event each within five seconds; no keeper restarted |

### B. The walls between two logins

| # | As | Command | Must be seen |
|---|---|---|---|
| 12 | `sam` | open a listening port in `robin`'s block on `$LO` and on `::1`; the same at `robin`'s door port while `robin`'s page is stopped | permission denied, each time |
| 13 | `sam` | the same from a job given to `at` and from a line in `crontab` | permission denied. **If it listens, the bind rule does not hold for a job the system starts outside a login's slice**: `systemd-cgls` shows where the job ran, and the proof in `doctor --server` must learn to try from there before it may say "proven" |
| 14 | `sam` | 200 times: open a port the kernel picks | never in anybody's block; `sysctl net.ipv4.ip_local_reserved_ports` names the whole range |
| 15 | `sam`, with a site of `robin`'s running | `curl` to it by `$LO`, by `::1`, by the server's public address and by its private-network address | refused, each time |
| 16 | `sam`, own computer | `ssh -L 9999:$LO:<robin's door> sam@<server>`, then `curl` to port 9999 | refused |
| 17 | `sam` | read `robin`'s home, state folder, a copy of the code, and `/proc/<a process of robin's>/environ` | refused; `ps` shows only `sam`'s processes, or doctor's note says they are not hidden |
| 18 | `admin` | a second file of sshd's with `Match User robin` and `PasswordAuthentication yes`; then `server harden` and `doctor --server` | **they must not stay green. Today they would**: both read what is in force for nobody in particular. Decide here whether both ask sshd for each login by name |
| 19 | `robin`, own computer | `ssh -A`, `ssh -X`, `ssh -R`, and a forward to another machine | each refused by the server |
| 20 | anywhere outside | a scan of the server's address | SSH and nothing else |

### C. Work

| # | Command | Must be seen |
|---|---|---|
| 21 | own computer: `whaleshark connect robin@<server>` | the page opens in the browser, signed in; in Safari too, at `http://whaleshark.localhost:7780/` |
| 22 | `whaleshark connect robin@<server> --terminals` | the window; the delay of a key and of a redraw, measured; copied text lands on the own computer; with agent and X11 forwarding switched on in the person's own ssh settings, `ssh-add -l` and `echo $DISPLAY` in a tab show nothing |
| 23 | server, as `robin`, in a project inside the home: `whaleshark task add ... --browser-check`, start a real agent, `whaleshark show <task>` | the lines for evidence and for the site; the site opens on the own computer within ten seconds |
| 24 | server, as `robin`: the prompt's screenshot command by hand, in that task's tab | a picture in the evidence folder that `report done` takes; while it runs, no browser process carries a "no sandbox" switch. **The step that tells whether the browser tooling, fetched by version alone and run as root by setup, is to be held to checksums and run without root** |
| 25 | server, as `robin`: `whaleshark init` in a folder outside the home | `outside_home` |
| 26 | both logins start agents up to the cap | the cap holds for each; memory per agent, with and without a browser, written down |
| 27 | close the laptop for an hour, open it | page and window come back without a key |

### D. The phone, by the private network (Tailscale)

| # | Command | Must be seen |
|---|---|---|
| 28 | `sudo whaleshark server phone --user robin`, then `--user sam`; `sudo reboot`; `sudo tailscale serve status` | two HTTPS ports forwarded, still there after the restart |
| 29 | as `robin`: `whaleshark dash phone on`; open the address on the phone | a number and nothing else |
| 30 | `whaleshark dash phone list`, `whaleshark dash phone approve <number>` at a plain terminal; once more from the action pane | the phone shows the page by itself |
| 31 | press a button on the phone; `whaleshark dash status` | the button works (a refusal as `wrong_origin` means the network rewrote the request's host name); the last caller is the phone's own identity |
| 32 | from another device of the network, `curl` to `robin`'s door with `robin`'s identity written into the header by hand, as `sam` | the item names `sam`: the network must drop a header it was sent |
| 33 | `sam`'s phone opens `robin`'s address | a number at most |
| 34 | `sam`: `tailscale serve` and `tailscale nc` towards `robin`'s door | refused for a login that is not root |
| 35 | phone: add the page to the home screen, "Nudge me on this phone"; `whaleshark doctor --notify` | the notice arrives on a locked iPhone and Android phone; note the text shown; a tap opens the page's own address |
| 36 | laptop closed; a worker asks; answer on the phone | the answer recorded as the phone's |

### E. The phone, by a public name behind Cloudflare's login, on the same server

| # | Command | Must be seen |
|---|---|---|
| 37 | `sudo whaleshark server phone --user robin --off`, the same for `sam` | the forwarding gone |
| 38 | `sudo whaleshark server tunnel --team <team>` | it says first that it installs a program that is not ours; the connector at the pinned version, running as its own login |
| 39 | `sudo whaleshark server phone --user robin --name ws1.<domain> --aud <tag> --email <address>`; the same for `sam` with `ws2` | the names made |
| 40 | anywhere, not logged in: `curl -sI https://ws1.<domain>/` | Cloudflare's login and nothing of ours in headers or body |
| 41 | phone: open the name, log in, pair as in 30; `whaleshark dash status --json` | the page; the pass's fields as the page expects them; the host name came unchanged |
| 42 | leave the page open ten minutes | it stays live |
| 43 | `sam`'s login at Cloudflare opens `ws1`; a pass of another application of the team sent through the tunnel, and straight to the door as the connector's login | refused by Cloudflare, by the connector and by the door |
| 44 | delete the Access application; set the server's clock five minutes wrong | everything refused, and the item appears |
| 45 | `sam`: twenty-one requests without a pass to `robin`'s door in a minute, while `robin`'s phone is in use | **today the door shuts for a minute to the right pass too. Decide here whether a shut door still lets a valid pass through** |
| 46 | steps 35 and 36 again | as there |
| 47 | `sudo whaleshark doctor --server`, both logins | clean under this way too |

### F. Write down

Each answer beside its step, and the sentence of the documentation it changes. A recorded real pass may hold a real address: whether it may be kept in the repository is the owner's to say.
