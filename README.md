# WhaleShark

WhaleShark (`whaleshark`) is a small command-line tool for running a team of coding agents. You talk to one lead agent; it hands tasks to worker agents, each in a terminal tab of its own; the tool keeps the record of who is doing what, who is stuck and what waits for you, and nothing counts as finished until a check has passed.

It is one program file with its own terminal engine. There is no desktop app, no service of ours on the internet, no telemetry and **no account**: WhaleShark never asks you to sign in. Your agent tool (Claude Code is the one it knows well) asks for its own sign-in.

## Status: not released

**There is no release yet, so none of the install lines below works today.** They are written as they will read. Until then the only way to run it is to build it (see Building).

What has been tried, plainly:

- The parts for one computer have been run from end to end **on macOS only**, by scripted scenarios and with real Claude Code agents on small tasks made for the try. Nobody has yet used it for real work.
- **Nothing of the server, the page, the phone or the tunnel has met a real server, a real phone, a Tailscale network, a Cloudflare account or a phone maker's push service.** Those parts are written from their makers' manuals and held by tests on one Mac, with a stand-in for every program of a server. The page has been opened in one browser on that Mac. Expect them to need mending at their first real use.
- It builds for Linux and Windows, and its tests are run on hosted machines of both. No person has used it on either, and nothing of it has run on a real Windows PC.
- A browser check has been played by a scripted agent only.
- A second kind of agent besides Claude Code is built from its maker's manual and has never been run.

## Install

Each system's package manager, or one line that needs none. All of them carry the same signed file.

**macOS**

```
brew install tgdigital-hub/whaleshark/whaleshark
```

or: `curl -fsSLO https://github.com/tgdigital-hub/whaleshark/releases/download/v0.1.0/install.sh && sh install.sh`

**Debian and Ubuntu**: the first install is three lines (the key, the source, the install), because the machine must first be told of our repository and its key.

```
curl -fsSL https://APT-ADDRESS/whaleshark.asc | sudo tee /etc/apt/keyrings/whaleshark.asc >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/whaleshark.asc] https://APT-ADDRESS stable main" | sudo tee /etc/apt/sources.list.d/whaleshark.list
sudo apt-get update && sudo apt-get install whaleshark
```

`APT-ADDRESS` is not chosen yet. Or, on any Linux: the `curl ... && sh install.sh` line above.

**Windows**

```
winget install whaleshark
```

Until winget has the package, Windows has no install line: take the Windows file from the release page, or build it.

`install.sh` needs no root and runs nothing it fetched. It takes the file for your system and the release's signed list of checksums, checks the list's signature against the release key it carries, checks the file against the list, and only then puts the program in `~/.local/bin`. Where that folder is not on your `PATH`, it says so. It prints the key's fingerprint; compare it with the one printed here. *(The release key does not exist yet; its fingerprint goes here with the first release.)* There is no `curl ... | sudo sh` line anywhere in this project.

**Then:** type `whaleshark` in your project, then your agent tool.

```
cd my-project
whaleshark          # sets the project up the first time, then opens the window
claude              # in the window; it asks for its own sign-in
```

`whaleshark` typed alone at the top of a git repository asks one question (which agent tools you use here), creates a private `.whaleshark` folder that git ignores, and opens the window: your tabs and panes in this terminal. Closing the window stops nothing. The lead agent finds its rules by itself and runs `whaleshark guide lead`; you tell it what you want.

`whaleshark help` lists all 42 commands, `whaleshark help <command>` shows one in full, and `whaleshark doctor` checks an installation line by line, each line with its fix.

The first worker started in a project you have never opened in Claude Code stops at Claude Code's own question whether you trust the folder. WhaleShark answers no prompt of another program, ever: say yes once yourself.

## A server for several people

One Linux machine (Ubuntu 22.04, Ubuntu 24.04 or Debian 12), one install, a login of the operating system for each person, and one administrator login that runs no agents. Five lines, and no shorter form: three on the server as the login your provider gave you, two **from your own computer**.

```
# on the server
curl -fsSLO https://github.com/tgdigital-hub/whaleshark/releases/download/v0.1.0/install.sh
sh install.sh
sudo ~/.local/bin/whaleshark server setup --admin ops

# from your own computer
ssh ops@SERVER sudo whaleshark server harden
ssh ops@SERVER sudo whaleshark server add --user ana
```

`server harden` closes the old doors (passwords, root login), and it runs only for someone who has just come in by the new one, so a wrong key locks nobody out. Then `sudo whaleshark doctor --server` proves the machine line by line. [docs/server.md](docs/server.md) says what each line does, how to connect from your computer, and what one person can see of another.

## The page and the phone

`whaleshark connect SERVER` on your own computer keeps one SSH connection to the server and opens the page in your browser: the same items, cards and buttons as the panes. The same page reaches a phone by one of two roads the administrator chooses, Tailscale or a public name behind Cloudflare's login. A phone is paired once, by a number you approve; a paired phone is nudged when you are needed, while `nudge.phone` is on. [docs/phone.md](docs/phone.md).

## A browser check

`whaleshark task add ... --browser-check` marks work that must be seen in a browser. WhaleShark starts no browser: the worker uses its own, saves screenshots, and neither its report nor `accept` passes without one. [docs/browser-check.md](docs/browser-check.md).

## What an agent can do, and what is protected

Read [docs/protection.md](docs/protection.md) before you give agents anything that matters. In one sentence: **an agent runs as you, and inside one login there is no wall**; the tool's rules stop mistakes, not intent, and the walls that hold are what you give the login and what your git host refuses.

## Uninstalling

Begin with the one step only the program can do: it takes out everything `init` ever wrote for your login (the rules block in your agent tool's instruction file, the hooks in each project's local settings).

```
whaleshark init --remove --all
whaleshark connect SERVER --not-at-login     # only if you used --at-login
whaleshark dash stop
whaleshark engine stop                       # ends every program in its panes, after asking
```

Then remove the program with what installed it (`brew uninstall whaleshark`, `sudo apt-get remove whaleshark`, `winget uninstall whaleshark`, or delete `~/.local/bin/whaleshark`), and delete what is left: `~/.config/whaleshark`, `~/.local/state/whaleshark`, `~/.cache/whaleshark` (on Windows the `whaleshark` folders under `%APPDATA%` and `%LOCALAPPDATA%`), and the `.whaleshark` folder of each project, which holds its runs and the tasks' copies of the code (its line in the repository's `.git/info/exclude` may go too). Branches a run made stay in your repository until you delete them. There is no command that undoes `server setup`; [docs/server.md](docs/server.md) lists what it put on the machine.

## Building

It needs Go 1.27 or newer and nothing else.

```
go build -trimpath -ldflags="-s -w" -o bin/whaleshark ./cmd/whaleshark
go test ./...
```

`go run ./build/ci` holds the tree to its limits and runs every check; `build/ci/README.md` says what each step does. [docs/design.md](docs/design.md) is a short note on how the program is put together.

## Dependencies

At most four modules, and no C compiler. A new one needs a written reason here. `NOTICE` carries their licences.

| Module | Why |
|---|---|
| `github.com/BurntSushi/toml` | reads `whaleshark.toml` and a person's own settings file |
| `golang.org/x/sys` | file locks, file-change notices, the console on Windows |
| `golang.org/x/term` | raw mode and the size of the terminal, for the panes |
| `github.com/rivo/uniseg` | how many cells a piece of text takes on the screen |

Five outside tools do the security checks of the gate. None is part of the program and the gate installs none; one that is missing is skipped on your own machine, said out loud, and fails a hosted run.

| Tool | For |
|---|---|
| [staticcheck](https://staticcheck.dev) | a stricter static analyser than `go vet` |
| [gosec](https://github.com/securego/gosec) | shell strings, weak file permissions, unchecked paths |
| [govulncheck](https://go.dev/doc/security/vuln/) | our modules against the public list of known flaws |
| [gitleaks](https://github.com/gitleaks/gitleaks) | secrets in the files and in the history |
| [shellcheck](https://www.shellcheck.net) | every shell script in the repository |

## Reporting a flaw

Privately, as `SECURITY.md` says.

## Licence

MIT; see `LICENSE`.

## Credits

WhaleShark borrows ideas from [Orca](https://github.com/stablyai/orca) (MIT licence, copyright Lovecast Inc.): a run, its tasks and their attempts as separate things, with a result bound to its attempt; the worker's rules carried in its prompt; an inbox that replays until it is acknowledged; a question that blocks, survives a timeout and resumes by its id; never taking silence for failure; errors that name the next command, and one command table behind help and validation; a guide the tool prints, with a short pointer to it in the agent's own instruction file; trying a merge with git before making it; and deleting a branch only when it is proven merged. The `.worktreeinclude` file is a convention taken from there too. No code and no text was taken from Orca or from any other project: every line here was written for WhaleShark.

The engine is written from the published standards and manuals for terminals. Its shape, a resident program that owns the terminals and a window that is only a view, is common to programs of this kind. The first builds ran on herdr, a separate terminal program, while the engine was being written, and the wish for a tool this light came from using it; none of its code is used here, and it is not needed to run WhaleShark.
