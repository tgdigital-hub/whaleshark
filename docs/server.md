# A server

**Nothing on this page has met a real server.** `server setup`, `server add` and `server upgrade` have run from nothing in containers of Debian 12 and Ubuntu 24.04, against the systems' real package sources. What a container cannot hold has only met stand-ins: the services, `server harden`, the firewall, and above all the walls between two logins. [platform/linux.md](platform/linux.md) says which is which, and holds the list for the first real servers. Read what each line does before you type it, and keep your provider's console within reach the first time.

## One person on a server, no root

For a Linux server where you are one plain login with no `sudo`, and somebody else runs the machine. Nothing here needs root; one line is for the server's administrator. It needs systemd, as Ubuntu and Debian have. **It has met stand-ins of `systemctl` and `loginctl` only, never a real server.**

```
# on the server, in an SSH session of your own
curl -fsSLO https://github.com/tgdigital-hub/whaleshark/releases/download/v0.1.0/install.sh
sh install.sh                       # the program in ~/.local/bin, for you alone; open a new session after it
cd ~/my-app
whaleshark init --agent claude --at-login
whaleshark doctor
whaleshark open
```

`--at-login` on Linux makes the keeper, which holds every tab and agent of yours, one of your own services: it writes `~/.config/systemd/user/whaleshark-keeper.service` and switches it on with `systemctl --user`. It starts the keeper unless one runs already; that one keeps your tabs, and after `whaleshark engine stop` the next `whaleshark open` starts the keeper as the service. From then on:

- **A keeper that dies is started again** (`Restart=on-failure`): after a crash, a kill or the machine running out of memory. It brings the tabs back, with each Claude Code agent resumed in its own conversation and every other pane as a shell over its last lines. `whaleshark engine stop` leaves it stopped, on purpose: that is how an upgrade goes, and the next `whaleshark open` starts it again.
- **Logging out ends nothing, and a restart of the machine starts the keeper again**, but only where the system *lingers* for you: keeps your services running while you have no session. `init` says whether it does. When it does not, the keeper stops when your last SSH session closes and starts when you log in again; many systems let you switch it on yourself with `loginctl enable-linger`, and where that is refused, ask the administrator to run, once, as root: `loginctl enable-linger <your login>`.
- **`ssh -t me@staging whaleshark open` attaches to the keeper that runs**, from any computer, and closing the window or the SSH connection leaves every agent at work. Where no keeper runs, `open` has your services start it rather than starting a second one beside them.

`whaleshark doctor` has two rows for it: "keeper runs as a user service" and "keeper survives logout (linger)", each yes or no with what to do. `whaleshark init --remove --all` switches the service off and takes its file out; a keeper that runs keeps running until `whaleshark engine stop`.

If `systemctl --user` answers that it cannot connect, your session has no service manager of its own: a login reached by `su` or `sudo -u` has none. Log in by SSH as yourself.

Everything else on this page is for a machine you set up yourself, with root.

## A server of your own

A server lets agents work while your laptop is shut. WhaleShark has no accounts of its own: each person is a login of the operating system, and everything that must be kept apart (files, terminals, agents, sign-ins, the page) is kept apart by that.

| Login | For | `sudo` | Runs agents |
|---|---|---|---|
| the administrator login (named `admin` unless you name another; `ops` in the lines below) | setting the machine up, adding people, upgrading | yes, by its own SSH key | never |
| a work login per person | everything else | never | yes |

## The five lines

A fresh Ubuntu 24.04 or Debian 12. Any other system is refused before anything is changed; Ubuntu 22.04 too, because its own git (2.34) is older than git 2.38, the least WhaleShark works with.

```
# on the server, as the login your provider gave you
curl -fsSLO https://github.com/tgdigital-hub/whaleshark/releases/download/v0.1.0/install.sh
sh install.sh
sudo ~/.local/bin/whaleshark server setup --admin ops

# from your own computer
ssh ops@SERVER sudo whaleshark server harden
ssh ops@SERVER sudo whaleshark server add --user ana
```

Where the program came from `apt-get install whaleshark`, the first two lines are the three of the README instead, and the third is `sudo whaleshark server setup --admin ops`.

1. **`install.sh`** needs no root. It checks the release's signature and the file's checksum, and installs the program for this one login.
2. **`server setup`** creates the administrator login with the SSH keys you came in with (`--admin-key FILE` gives another), with `sudo` and no password. It installs, from the system's own package source, git, curl, the firewall and automatic security updates, with automatic restarts of the machine switched off; puts the program in `/usr/local/bin`, owned by root; installs Claude Code from its maker's signed package source; and installs a pinned Node and a headless Chromium under `/opt/whaleshark` for browser checks. It writes `/etc/whaleshark/server.toml`. **It closes nothing**: the door you came in by still works.
3. **`server harden`** makes SSH take keys only, with no passwords, no login as root and no agent forwarding, reads back what is in force and stops if a value differs; and sets the firewall to refuse everything incoming except SSH. It refuses to run unless the administrator login called it, over SSH, which is why you type it from your own computer: if your key does not work, you never reach it, and nothing has been closed.
4. **`server add --user NAME [--key FILE]`** creates a work login with no `sudo`, a home nobody else can enter, a block of 1,000 ports of its own, a share of memory and processes, and two services that run while nobody is logged in: the keeper, which holds that login's terminals, and the page's server. Without `--key` the login gets the administrator's keys. Run it again for each further person; a machine holds eleven.

Everything that runs as root is the program `install.sh` verified, running a script compiled into it. Two steps are an exception, and it is said here because the rule is otherwise kept: the Node archive is checked against a checksum, but the browser's installer that Node then runs, as root, is pinned by its version only.

Each person then signs in to Claude Code once in their own login, sets their git name and email, and gives the login a narrow token ([protection.md](protection.md)).

## Proving it

```
sudo whaleshark doctor --server     # the machine and the calling login
whaleshark doctor --server          # as a work login: that login
```

One line per check, each with its fix: what answers from the network, SSH's values in force, the two kinds of login, updates, the program file, private folders, the port block, temp files, the keeper, the browser and its sandbox, the page, the way in for a phone. `whaleshark server status` lists the logins, their blocks, what each is running, and whether the machine wants a restart.

**The port rules are the part to read first.** A login's block of ports is meant to be that login's alone, both to open and to connect to, by two settings of the system. `doctor --server` tries both as a second login and writes "proven" only then. Until it does, another person on the machine may reach a site your agent runs, or put a page of their own where your agent's browser will load it: do not share a server whose port rules are unproven.

## Working from your own computer

Install the program on your own computer too, then:

```
whaleshark connect ana@SERVER                # the page, in your browser
whaleshark connect ana@SERVER --terminals    # the window, in this terminal
```

`connect` takes whatever you would give `ssh`. It keeps one SSH connection with agent and X11 forwarding off, forwards only ports of the tool's own range (20000 to 31999) whatever the server names, opens the page signed in, forwards the site of each live task under its own port number so that a link an agent prints works as written, and makes the connection anew after sleep or a change of network. `--terminals` shows the server's tabs and panes; typing `ssh -t ana@SERVER whaleshark open` yourself does the same. On macOS, `--at-login` starts the page's connection when you log in and `--not-at-login` undoes it.

Nothing on the server belongs to the connection. Agents, the lead agent and the page run under the login's own services whether anybody is connected or not.

On a server a project must lie inside the login's own home, and `init` refuses a folder another login can write to. Share work through git, never through a shared folder.

## What one person can see of another

| Of another login | Visible? |
|---|---|
| files, briefs, results, screenshots, settings, tokens, copies of the code | no: a private home |
| terminals, panes, agents, the page | no: socket files in their home |
| a site one of their agents runs | no once the port rules are proven; **yes until then** |
| processes, with task names and paths on their command lines | possibly; hiding them is a best effort. WhaleShark puts no secret on a command line |
| that they are logged in, and the machine's load | yes |

The administrator, and a flaw in the system's kernel, cross every one of these.

## Upgrading

As the administrator, fetch the new version's `install.sh` and run it as yourself, then `sudo ~/.local/bin/whaleshark server upgrade`; where the program came from apt, `sudo whaleshark server upgrade` alone asks apt for the new one. It shows what `server status` shows, puts the verified new version in place for everyone, and restarts each login's page. Programs already running keep the file they started with, and a keeper is never restarted by it, because that would end every agent of its login. Each open run gets one `upgraded` event that tells its lead agent to start waiting again. There is no self-update, and the program never looks for new versions.

## What it put on the machine

There is no command that undoes `server setup`. What it and `server add` wrote: the administrator login and each work login; `/etc/whaleshark/`; `/usr/local/bin/whaleshark`; `/opt/whaleshark/`; `/etc/ssh/sshd_config.d/00-whaleshark.conf`; `/etc/sudoers.d/whaleshark-admin`; `/etc/apt/apt.conf.d/52whaleshark`; `/etc/sysctl.d/60-whaleshark.conf`; the two service entries, `whaleshark-keeper.service` and `whaleshark-dash.service`, under `/etc/systemd/user/`; Claude Code's package source and package; the firewall's rules. The Cloudflare way adds a login and a service named `whaleshark-tunnel` and Cloudflare's package, which `sudo whaleshark server tunnel --off` takes away.

## A stolen laptop

Until you remove its key, whoever holds your laptop is you. Protect the SSH key with a passphrase or a hardware key. To end it: delete the key's line from `~/.ssh/authorized_keys` in your login on the server (and in the administrator's, if it held that key too), run `whaleshark dash stop --forget` there, which signs every browser and phone out.
