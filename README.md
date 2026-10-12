# WhaleShark

WhaleShark (`whaleshark`) is a small command-line tool for running a team of coding agents. You talk to one main agent, the orchestrator; it hands tasks to worker agents, each in a terminal tab of its own; the tool keeps the record of who is doing what, who is stuck and what waits for you, and nothing counts as finished until a check has passed.

It is one program file with its own terminal engine. There is no desktop app, no service of ours on the internet, no telemetry and **no account**: WhaleShark never asks you to sign in. Your agent tool (Claude Code is the one it knows well) asks for its own sign-in.

## How it works

### What you type first

```
cd my-project
whaleshark          # sets the project up the first time, then opens the window
claude              # in the window; it asks for its own sign-in
```

`whaleshark` typed alone at the top of a git repository asks one question (which agent tools you use here), creates a private `.whaleshark` folder that git ignores, and opens the window in this terminal. Then you start your agent tool in the first tab and tell it, in your own words, what you want done. Nothing else has to be learned to begin: the agent finds its rules by itself, opens a job, splits the work, starts the workers and tells you when something needs you.

### What you see

Roughly this, in a terminal 100 columns wide:

```
 WHALESHARK        ▲1 │ Orchestrator │ sign-up page │ photo upload │ price list
                      ├──────────────────────────────────────────┬─────────────────────────────
 WORKSPACES           │                                          │ FLEET  3 agents · 1 needs you
 ▌▲ shop-site       4 │  (your conversation with the             │
  · other tabs      1 │   orchestrator)                          │ ▲ sign-up page      needs you
                      │                                          │   ████████░░░░░░░░  40%
 AGENTS · shop-site   │  > Three workers are running. One of     │ ◐ photo upload       building
 ▌● orchestrator      │    them asks a question; it is in the    │   ██████████░░░░░░  55%
  ▲ sign-up page      │    pane below.                           │ ● price list         to check
  ◐ photo upload      │                                          │   ████████████████ 100%
  ● price list        ├──────────────────────────────────────────┤
                      │ ACTIONS  1 waiting                       │
 [ Menu ] [ Settings ]│   ▲ sign-up page · a question            │
 live · 2s            │     Must the old link keep working?      │
```

- **The tab row** is on top. The first tab is the main agent's and is called **Orchestrator**. Every worker gets a tab of its own, named after its task. When you give the chat a name in Claude Code (`/rename`), the Orchestrator tab takes that name. You can click into any tab and look or type; closing the window stops nothing.
- **The left section** is always there. On top it lists your workspaces (the projects that have a job or a tab open); under them, the agents of the one in front, each with a mark for its state. Click a row and you are in that tab. `▲` with a number at the very top means that many things wait for you. In a narrow window it shrinks to a strip of marks. It can stand at the left or the right edge, and it cannot be switched off.
- **The fleet pane** (right, in the picture) shows one card for each worker: what it is doing, how far it is, and whether it needs you.
- **The action pane** (below) holds everything that waits for you: a worker's question, a sign-off the orchestrator asks for, an agent that sits at a permission prompt. You answer there, by a click or by keys, and the answer goes to whoever asked. Its first line has `[ Stop all ]`, which stops every agent at its next step, and `[ Resume ]`.

Both panes open by themselves when a job begins. Each can be moved or switched off: the fleet pane right, left, at the bottom or off; the action pane below, on top or off.

### The settings screen

`[ Settings ]` at the foot of the left section (or `whaleshark ui settings`) opens one screen with every setting the program has: the colours, where the panes sit, sounds and pop-ups, the keys, Fleet Mode and its limits, the agent tools, this project's own settings. A setting is changed by a click or by the arrow keys and is saved as you go; one key takes the last change back. A setting that names a program to run shows you exactly what it would run and asks first. Every setting is also a key of `whaleshark set`, and `whaleshark help set` lists them.

### Fleet Mode

For a few tasks, the orchestrator runs the workers itself. For many, switch Fleet Mode on (Settings › Fleet Mode, or `whaleshark set fleets.mode on`; it holds from the next job). The orchestrator then sorts your work into groups that belong together and gives each group to a **fleet**: a fleet leader, which is another agent in a tab named after the fleet, and that leader's own workers.

```
                        you
                         │   you speak only to the orchestrator
                         ▼
                  ┌──────────────┐
                  │ ORCHESTRATOR │   groups your work, starts leaders, sees all fleets
                  └──┬────┬────┬─┘
          ┌──────────┘    │    └──────────┐      the orchestrator speaks only to leaders
          ▼               ▼               ▼
   ┌────────────┐  ┌────────────┐  ┌────────────┐
   │ LEADER     │  │ LEADER     │  │ LEADER     │   each decides how, and with how many workers
   │ storefront │  │ warehouse  │  │ help pages │   each may be a different agent tool
   └─┬───┬───┬──┘  └─┬───┬───┬──┘  └─┬───┬──────┘
     ▼   ▼   ▼       ▼   ▼   ▼       ▼   ▼
     w   w   w       w   w   w       w   w         workers speak only to their own leader

   a question climbs one step at a time: worker → its leader → the orchestrator → you;
   the answer goes back down the same steps. A permission prompt in any tab is always yours.
```

The left section then shows three levels (the orchestrator, each fleet, each fleet's workers), and the fleet pane shows one card for each fleet. Three limits are yours to set: how many fleets run at once, how many agents one fleet may have, and how many agents run in all. A start over a limit is refused and says which.

Fleets of one project are not walled off from each other: two fleets may change the same file. What keeps them from undoing each other is what the tool does for any two workers: it notices when two pieces of work touch the same file and says so, the later one is told to bring the other's change in first, and nothing is collected until its check has passed on the merged work.

### Fleets you keep

A fleet normally lasts as long as its work. You can also keep one, by name, with a sentence on what it works on. Say it to the orchestrator in your own words:

> Create a fleet for the storefront, and one for the warehouse screens.

It saves both (`whaleshark run fleet "storefront" "everything of the storefront: its pages and its checkout"`). From then on:

> Here are ten things for the storefront: …

goes to the storefront fleet, a list for the warehouse screens goes to the warehouse fleet, and a mixed list is split between them. Work that fits no fleet you keep gets a fleet made for it on the spot, which is gone again when that work is done; a fleet you keep stays, in the left section and in `whaleshark status`, until you say it may go. `whaleshark run fleet` lists the ones you keep.

### Agent tools

Claude Code is the tool that has been run for real. Others are wired in by a description, in Settings › Agent tools: a name, the command that starts it (a full path, which you are shown and asked about before it is used), how a model is named for it, the file it reads when it starts, and a sentence on what it suits. `[ Try it ]` starts it once in a tab of its own and says what it saw. With more than one tool wired in, the orchestrator is shown each with your sentence and may name one for a fleet (how well a real orchestrator chooses has not been tried with a real second tool). A project you download can never wire in a tool: only you can, on your own machine.

So that an agent knows its part without being told, `whaleshark init` writes a short block into the file each tool reads when it starts (for Claude Code, `CLAUDE.md` in its own folder under your home folder). The block says how the team is built, how an agent finds out whether it is the orchestrator, a fleet leader or a worker, and which guide to read: `whaleshark guide lead`, `whaleshark guide leader` or `whaleshark guide worker`. A worker or a leader is also told by its first prompt. `whaleshark init --remove` takes the block out again.

## Status: not released

**There is no release yet, so none of the install lines below works today.** They are written as they will read. Until then the only way to run it is to build it (see Building).

What has been tried, plainly:

- The parts for one computer have been run from end to end **on macOS only**, by scripted scenarios and with real Claude Code agents on small tasks made for the try: a real orchestrator with three workers, and with Fleet Mode on a real orchestrator, four fleet leaders and their workers, a question that climbed from a worker to the person and back, a leader that was ended and replaced, Stop all, and two fleets that changed one file. Those tries found faults, which are mended; the window was looked at as text only, in a terminal of the try's own. **Nobody has yet used it for real work, and no person has yet sat in front of the left section, the settings screen or Fleet Mode.**
- **Nothing of the server, the page, the phone or the tunnel has met a real server, a real phone, a Tailscale network, a Cloudflare account or a phone maker's push service.** The server's set-up has run from nothing in containers of Debian 12 and Ubuntu 24.04, against the systems' real package sources; what a container cannot hold (the services, the firewall, the walls between two logins) is held by tests with stand-ins, and both roads for a phone are written from their makers' manuals. The page has been opened in one browser on one Mac. Expect them to need mending at their first real use. [docs/platform/linux.md](docs/platform/linux.md) says what is proven and gives the list for the first real servers.
- It builds for Linux and Windows, and every push runs its tests on hosted machines of both, with a proof of its own for each: [docs/platform/linux.md](docs/platform/linux.md), [docs/platform/windows.md](docs/platform/windows.md), and the engine call by call on all three systems in [docs/platform/engine-matrix.md](docs/platform/engine-matrix.md). No person has used it on either, **nothing of it has run on a real Windows PC**, and the Windows page lists what stays a limit there and the checklist for the first real one.
- A browser check has been done by a real agent once, on that Mac; on Linux and Windows its one command has been run by tests only.
- Replacing the program under a running job has been tried on that Mac: the lead agent's `wait` ended with the event `upgraded` a few seconds after the file was replaced, and the next `wait` went on.
- **Claude Code is the only agent tool that has been run.** Codex's description is built from its maker's manual and has never been run. Wiring in a tool by a description has been tried with a stand-in program only: a small script that behaves as an agent with no hooks, tried by `[ Try it ]` and as the leader of a fleet. What a real second tool shows on its screen, takes as its first line and does with a file of rules is unproven.
- The tab follows a chat's name only with a tool whose title says so (Claude Code does). The title does not tell a name you gave from one the tool chose by itself, so the tab takes a name only when it changes while the agent is at rest; a rename typed within ten seconds after the agent finished is missed.

## Install

Each system's package manager, or one line that needs none. All of them carry the same signed file. WhaleShark works with git 2.38 or newer; with an older one `init` sets nothing up and says so, and `whaleshark doctor` has the row.

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

`APT-ADDRESS` is not chosen yet: until the repository is served somewhere, these three lines stay as they will read, and a release carries the same folder, key and all, in its `packages.tar.gz`. The key's fingerprint is `F997 9264 BFC0 FF2F CD5D 803E 6472 B33B 2405 C475`; `gpg --show-keys whaleshark.asc` prints it. Or, on any Linux: the `curl ... && sh install.sh` line above.

**Windows**

```
winget install whaleshark
```

Until winget has the package, Windows has no install line: take `whaleshark-windows-amd64.exe` and the signed list from the release page, check them by hand (`ssh-keygen -Y verify` as `build/release/README.md` shows, then the file's checksum against the list), and put the file on your `PATH` as `whaleshark.exe`; or build it. Typed commands work in `cmd`, both PowerShells and git's bash.

`install.sh` needs no root and runs nothing it fetched. It takes the file for your system and the release's signed list of checksums, checks the list's signature against the release key it carries, checks that the list is the one of its own version (its first line says which), checks the file against the list, and only then puts the program in `~/.local/bin`. Where that folder is not on your `PATH`, it says so. It prints the key's fingerprint; compare it with the one printed here (the script itself is the one file nothing checks before it runs; [docs/protection.md](docs/protection.md) shows how to check it by hand first):

```
SHA256:P0bN9YsEy+jMZIIPiVGKVIsbVEOi7FQ/W0k7iwRTlRE
```

The release key is an SSH key, and this is its public half as the one line `ssh-keygen -Y verify` wants in a file of allowed signers (`build/release/README.md` shows the whole check by hand):

```
release namespaces="file" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAASRVefb9n1zHgxy4uZYqS0BXTAiBpjYnXDNDa8q47Z
```
 There is no `curl ... | sudo sh` line anywhere in this project.

**Then:** type `whaleshark` in your project, then your agent tool, as "What you type first" shows above. Closing the window stops nothing.

On Windows, `whaleshark init --at-login` has the engine started when you log on, so your tabs are there before you open the window: one line in your login's own list of what Windows starts at log-on, which needs no administrator, and which `whaleshark init --remove --all` takes out again. On a Mac the first `whaleshark open` starts the engine; on a server its set-up starts it with the machine.

`whaleshark help` lists all 42 commands, `whaleshark help <command>` shows one in full, and `whaleshark doctor` checks an installation line by line, each line with its fix.

The first worker started in a project you have never opened in Claude Code stops at Claude Code's own question whether you trust the folder. WhaleShark answers no prompt of another program, ever: say yes once yourself.

## A server for several people

One Linux machine (Ubuntu 24.04 or Debian 12), one install, a login of the operating system for each person, and one administrator login that runs no agents. Five lines, and no shorter form: three on the server as the login your provider gave you, two **from your own computer**.

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

At most four modules, and no C compiler. A new one needs a written reason here. `NOTICE` carries their licences, and goes with every release beside `LICENSE`, both named in the signed list; the Debian package holds both as its copyright file.

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
