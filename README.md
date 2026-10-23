# WhaleShark

WhaleShark (`whaleshark`) is a small command-line tool for running a team of coding agents. You talk to one main agent, the orchestrator; it hands tasks to worker agents, each in a terminal tab of its own; the tool keeps the record of who is doing what, who is stuck and what waits for you, and nothing counts as finished until a check has passed.

It is one program file with its own terminal engine. There is no desktop app, no service of ours on the internet, no telemetry and **no account**: WhaleShark never asks you to sign in. Your agent tool (Claude Code is the one it knows well) asks for its own sign-in.

## Install in one line

```
# macOS, in Terminal
curl -fsSL https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.sh | sh
# Linux
curl -fsSL https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.sh | sh
# Windows, in PowerShell (the second line is the same from cmd)
irm https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.ps1 | iex
powershell -c "irm https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.ps1 | iex"
```

Then open a new terminal and type `whaleshark`. Homebrew, apt, winget, one fixed version and checking the files by hand are under [Install](#install). There is no release yet: see [Status](#status-not-released).

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
- **The left section** is always there. On top it lists your workspaces (the projects that have a job or a tab open); under them, the agents of the one in front, each with a mark for its state. Click a row and you are in that tab. `▲` with a number at the very top means that many things wait for you. In a narrow window it shrinks to a strip of marks. It can stand at the left or the right edge, and it cannot be switched off. For a newcomer it begins with five first steps (start your agent, tell it what you want, where agents appear, where questions land, the two keys worth knowing); each ticks itself when it happens, `[ hide ]` ends them, and Settings › Look brings them back.
- **The fleet pane** (right, in the picture) shows one card for each worker: what it is doing, how far it is, and whether it needs you.
- **The action pane** (below) holds everything that waits for you: a worker's question, a sign-off the orchestrator asks for, an agent that sits at a permission prompt. You answer there, by a click or by keys, and the answer goes to whoever asked. Its first line has `[ Stop all ]`, which stops every agent at its next step, and `[ Resume ]`.

Both panes open by themselves when a job begins. Each can be moved or switched off: the fleet pane right, left, at the bottom or off; the action pane below, on top or off.

### The settings screen

`[ Settings ]` at the foot of the left section (or `whaleshark ui settings`) opens one screen with every setting the program has: the colours, where the panes sit, sounds and pop-ups, the keys, Fleet Mode and its limits, the guardrails, the agent tools, this project's own settings. A setting is changed by a click or by the arrow keys and is saved as you go; one key takes the last change back. A setting that names a program to run shows you exactly what it would run and asks first. Every setting is also a key of `whaleshark set`, and `whaleshark help set` lists them.

You can also ask your orchestrator to list the settings, explain one or change one. A harmless one (a colour, where a pane sits) it changes at once, and every screen carries a line saying so until you have looked. One that holds agents or is how you are called (the limits, the guardrails, nudges, do not disturb, the command key that reaches Stop all) becomes an item that waits for your own click. One that names a program to run, whether a program may write your clipboard, and everything of the phone's door, it cannot set at all; nor can a request from a browser or an app. Fleet leaders and workers change no setting.

`[ Save my settings to a file ]` under Look writes your settings as one file to carry to another computer (`whaleshark set --save FILE`). It holds no key, no paired phone, no approval and nothing of a project. `whaleshark set --load FILE` shows every change first, takes one typed yes for the plain ones, and asks about each line that would run a program by itself. Two changes that run nothing are asked about one by one in the same way: a tool's hooks, without which Stop all and the guards do not hold its agents, and the file `whaleshark init` writes a tool's rules into. That file is one that is not there yet or one that holds those rules already; any other file of your home folder is refused, also when you set it by hand. A setting the file does not name stays as it is. A file from somebody else is theirs: read the list before you type yes.

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

### Guardrails

Settings › Guardrails holds four safeguards. Three come switched off, and with them off a job runs as everything above describes.

- **Plan first, then start** (off). On: the orchestrator must show you its plan (which fleets, which tasks, how many agents, which tool), and nothing starts until you approve it. What it adds later is shown the same way, and so is a task whose brief or check is changed after your yes: it is shown again before it starts.
- **Protected files** (off). You keep a list of paths that matter (it starts with secrets files, database migrations, deploy settings; a project may add to it and never take from it). On: agents work as usual, but a job that changed such a file cannot be landed until you have approved the file as it is. Changed again after your yes, it asks again. A path counts whatever the case of its letters, a file moved away counts, and so does a link put where a listed folder would begin. It guards `whaleshark land` and no other way into your main branch.
- **Spending guard** (off). On: the tool counts agent-hours, one agent open for one hour. At four fifths of your limit the orchestrator is told to wrap up. At the limit the job's agents are held where they stand, those with nothing in hand are closed, and you are asked: more hours, no limit for this job, or leave it held. One fleet can have a limit of its own; the limit is a job's, and a new job begins a new count. **It counts time, not tokens or money**: agent tools do not report those reliably. Agents are held as Stop all holds them, so a tool whose hooks WhaleShark does not read runs on until its next `whaleshark` command.
- **Stuck detector** (on). It notices an agent that is going nowhere: long work with no progress note, the same check failing three times, the same note repeated, a result sent back three times. It tells the agent's leader, ten minutes later the orchestrator, ten minutes later you. Its spans of time are the project's own (`[limits]` in `whaleshark.toml`), which come with a repository and need no approval; while the detector is on, each is used up to a ceiling of its own, so no project's file makes it quiet: four hours without a progress note, a quarter of an hour at rest, and four hours for a task whose limit is set above that. A task with no time limit keeps none. It never stops an agent, and one missed note is no alarm: a failing check or a result sent back three times is enough alone, any other sign takes a second beside it or twice its time. The figures are reasoned, and one try with real agents gave no ground to change them: no false alarm in about two and a half hours of small tasks. That is not a measurement; real leaders dealt with a failing check and a silent worker themselves before the detector had anything to say.

The three approvals (a plan, protected files, more agent-hours) are yours alone: the orchestrator cannot give them or pass them on. Like every rule of this tool they stop mistakes, not intent; [docs/protection.md](docs/protection.md) says what an agent that goes wrong can still do.

`whaleshark log --export` (or `[ Export ]` in the same section) writes one plain file for a job: who did what, what you approved and from where, and the agent-hours it used. Its second line reminds you that it holds what your agents wrote.

### Agent tools

Every agent tool runs in a terminal, so WhaleShark can start any of them, hand it its task and read its screen. What differs is how much it knows of what the agent is doing, whether Stop all holds it, and whether it is picked up after a restart. So tools are listed with one of three ranks, in `whaleshark doctor`, in the settings screen and to the orchestrator: **proven** (Claude Code, the only tool that has been run for real), **ready, not proven** (Codex: its description ships, written from its maker's manual, and has never been run), and **works as a terminal tool** (any other you wire in). All but the first are experimental. Others are wired in by a description, in Settings › Agent tools: a name, the command that starts it (a full path, which you are shown and asked about before it is used), how a model is named for it, the file it reads when it starts, and a sentence on what it suits. `[ Try it ]` starts it once in a tab of its own and says what it saw; the list then shows what worked on your machine, and when. With more than one tool wired in, the orchestrator is shown each with your sentence and may name one for a fleet (how well a real orchestrator chooses has not been tried with a real second tool). A project you download can never wire in a tool: only you can, on your own machine.

Gemini CLI, Qwen Code, OpenCode, Cursor CLI, Hermes Agent, Goose, Crush and Amp are built in as well, ready and not proven like Codex; [docs/agents.md](docs/agents.md) has the table of every tool, how to set each up, how to use a local model or one on another machine, and ready-made files for others.

So that an agent knows its part without being told, `whaleshark init` writes a short block into the file each tool reads when it starts (for Claude Code, `CLAUDE.md` in its own folder under your home folder). The block says how the team is built, how an agent finds out whether it is the orchestrator, a fleet leader or a worker, and which guide to read: `whaleshark guide lead`, `whaleshark guide leader` or `whaleshark guide worker`. A worker or a leader is also told by its first prompt. `whaleshark init --remove` takes the block out again.

### How work lands

Each task works in a copy of the code of its own. As it comes, `accept` collects each task's work on one branch for the job, and `whaleshark land` brings the whole of it to your branch, as one pull request or a local merge. With `[land] mode = "pr-per-task"` in `whaleshark.toml` each accepted task is pushed as a pull request of its own instead, watched in the background, and merged by itself once its checks pass, one at a time; a red check goes back to its worker. `[worktrees] fetch_base = true` fetches the base branch before a job begins and before each task starts, so that work starts from where the remote is, and leaves your own checkout as it is.

## Status: not released

**There is no release yet, so none of the install lines below works today.** They are written as they will read. Until then the only way to run it is to build it (see Building).

What has been tried, plainly:

- The parts for one computer have been run from end to end **on macOS only**, by scripted scenarios and with real Claude Code agents on small tasks made for the try: a real orchestrator with three workers, and with Fleet Mode on a real orchestrator, four fleet leaders and their workers, a question that climbed from a worker to the person and back, a leader that was ended and replaced, Stop all, and two fleets that changed one file. Those tries found faults, which are mended; the window was held by a terminal of the try's own and read as text, and in the last try also drawn as pictures with its colours, in the dark scheme, the light one and with none. **Nobody has yet used it for real work, and no person has yet sat in front of the left section, the settings screen or Fleet Mode.**
- **Nothing of the server, the page, the phone or the tunnel has met a real server, a real phone, a Cloudflare account or a phone maker's push service.** The server's set-up has run from nothing in containers of Debian 12 and Ubuntu 24.04, against the systems' real package sources; what a container cannot hold (the services, the firewall, the walls between two logins) is held by tests with stand-ins, and the road for a phone is written from its maker's manual. The page has been opened in one browser on one Mac. Expect them to need mending at their first real use. [docs/platform/linux.md](docs/platform/linux.md) says what is proven and gives the list for the first real servers.
- It builds for Linux and Windows, and every push runs its tests on hosted machines of both, with a proof of its own for each: [docs/platform/linux.md](docs/platform/linux.md), [docs/platform/windows.md](docs/platform/windows.md), and the engine call by call on all three systems in [docs/platform/engine-matrix.md](docs/platform/engine-matrix.md). No person has used it on either, **nothing of it has run on a real Windows PC**, and the Windows page lists what stays a limit there and the checklist for the first real one.
- **The guardrails, settings through the orchestrator, the saved profile, the first steps and the interface for apps have been tried once with real Claude Code agents**, on that Mac, on small tasks made for the try, beside the scripted scenarios. A real orchestrator listed and explained the settings, changed a harmless one and had to wait for a click for a guardrail; a worker was refused; a plan was shown, sent back and approved; a protected file held a landing until it was approved, twice; a job was warned at four fifths of one agent-hour, held at the limit and let go; a file that told the orchestrator to switch the guard off was declined. Twenty-two faults were found and mended, among them that agents held by the guard took the line that ended the hold and then waited to be told again. The stuck detector was on throughout and raised no alarm while anything was at work or waiting for an answer; when a worker was told to do nothing and everybody was told to leave it, the verdict came after 24 minutes, the lead agent was told, and two minutes later the person. **Two things that try found not to work as meant were built after it.** A note sent to the orchestrator from the page, a phone or `whaleshark tell lead` arrived, and a real Claude Code did not act on it, because it did not come from the person in its chat: your own note is now typed into the orchestrator's chat as one line that says where it came from ("typed at a terminal: …", "from the page: …", "from your phone: …"), and one real orchestrator answered two such lines at once; a real browser or phone has not sent one, and a note that has to wait for a busy agent has been tried by tests only. The spending guard's count was not exact (a leader that holds two groups at once counted twice, and a fleet's hours were forgotten when it ended and was given work again): it now counts by tab and keeps a fleet's earlier hours, which the tests hold and no real agents have tried again. No person has read the descriptions on the real screen, and the notice of a new version has never asked the real GitHub from the program (there is no release for it to find).
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

or, with nothing but `curl` (on any Linux too):

```
curl -fsSL https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.sh | sh
```

or, for one version: `curl -fsSLO https://github.com/tgdigital-hub/whaleshark/releases/download/v0.1.0/install.sh && sh install.sh`. With the release's files already in a folder: `sh install.sh --from DIR`.

**Debian and Ubuntu**: the first install is three lines (the key, the source, the install), because the machine must first be told of our repository and its key.

```
curl -fsSL https://APT-ADDRESS/whaleshark.asc | sudo tee /etc/apt/keyrings/whaleshark.asc >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/whaleshark.asc] https://APT-ADDRESS stable main" | sudo tee /etc/apt/sources.list.d/whaleshark.list
sudo apt-get update && sudo apt-get install whaleshark
```

`APT-ADDRESS` is not chosen yet: until the repository is served somewhere, these three lines stay as they will read, and a release carries the same folder, key and all, in its `packages.tar.gz`. The key's fingerprint is `F997 9264 BFC0 FF2F CD5D 803E 6472 B33B 2405 C475`; `gpg --show-keys whaleshark.asc` prints it. Or, on any Linux: the `curl ... | sh` line above.

**Windows**

```
winget install whaleshark
```

Until winget has the package, one line in PowerShell (Windows PowerShell 5.1 or PowerShell 7):

```
irm https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.ps1 | iex
```

or, for one version: `irm https://github.com/tgdigital-hub/whaleshark/releases/download/v0.1.0/install.ps1 | iex`. From `cmd`: `powershell -c "irm https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.ps1 | iex"`. With the release's files already in a folder: `powershell -ExecutionPolicy Bypass -File install.ps1 -From DIR`.

`install.ps1` is the twin of `install.sh` below and makes the same checks with the same key, using the `ssh-keygen` of the OpenSSH client that Windows 10 and 11 ship (where it is missing, it says how to add it). It needs no administrator, puts the program at `%LOCALAPPDATA%\Programs\whaleshark\whaleshark.exe`, adds that folder to your login's `PATH` once, and takes off the mark Windows puts on a downloaded file. Then open a new terminal and type `whaleshark`. Typed commands work in `cmd`, both PowerShells and git's bash.

`install.sh` needs no root and runs nothing it fetched. It takes the file for your system and the release's signed list of checksums, checks the list's signature against the release key it carries, checks that the list is the one of its own version (its first line says which), checks the file against the list, and only then puts the program in `~/.local/bin`. Where that folder is not on your `PATH`, it adds one line for it, once, to the start files of your login's shell (zsh: `~/.zprofile` and `~/.zshrc`; bash: `~/.bash_profile`, or `~/.profile` where there is none, and `~/.bashrc`; fish: `fish_add_path`; any other: `~/.profile`), says which, and tells you to open a new terminal. Running it again upgrades the program in place and changes no start file. It prints the key's fingerprint; compare it with the one printed here (the script itself is the one file nothing checks before it runs; [docs/protection.md](docs/protection.md) shows how to check it by hand first):

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

One Linux machine (Ubuntu 24.04 or Debian 12), one install, a login of the operating system for each person, and one administrator login that runs no agents. Four lines, and no shorter form: two on the server as the login your provider gave you, two **from your own computer**.

```
# on the server
curl -fsSL https://github.com/tgdigital-hub/whaleshark/releases/latest/download/install.sh | sh
sudo ~/.local/bin/whaleshark server setup --admin ops

# from your own computer
ssh ops@SERVER sudo whaleshark server harden
ssh ops@SERVER sudo whaleshark server add --user ana
```

For one fixed version, the first line is `curl -fsSLO https://github.com/tgdigital-hub/whaleshark/releases/download/v0.1.0/install.sh && sh install.sh`.

`server harden` closes the old doors (passwords, root login), and it runs only for someone who has just come in by the new one, so a wrong key locks nobody out. Then `sudo whaleshark doctor --server` proves the machine line by line. [docs/server.md](docs/server.md) says what each line does, how to connect from your computer, and what one person can see of another.

## The page and the phone

`whaleshark connect SERVER` on your own computer keeps one SSH connection to the server and opens the page in your browser: the same items, cards and buttons as the panes. The same page reaches a phone by a public name behind Cloudflare's login, which the administrator sets up once. A phone is paired once, by a number you approve; a paired phone is nudged when you are needed, while `nudge.phone` is on. [docs/phone.md](docs/phone.md).

For apps, which do not exist: the phone's door also serves the same things as data, under a numbered address (`/api/1/`), and `whaleshark dash phone on` (or its row in Settings › Page & phone) shows a pairing code that is good once, for one minute, and still needs your approval of a number. An app could read every job, card and setting, press the buttons the page has and send the orchestrator a message, which is typed into the orchestrator's chat as one line that begins "from your phone:"; it could never send a key, type into any other tab, set what names a program or approve a project's settings. No app exists. A small script has spoken it once as an app would, on one computer's own address and behind a pass the try signed itself: it paired by the code, read the jobs, a task, the orchestrator's screen and the settings, pressed buttons, changed settings and was refused the one that names a program. Its message reached the orchestrator, which did not act on it; such a message is typed into the orchestrator's chat since (see Status), and no script or app has sent one that way yet. An app cannot be nudged yet: that needs a relay which is not built.

## A notice that a new version is out

Once a day, when you open the window, WhaleShark asks GitHub for the number of the latest release and shows one dim line if yours is older. The request carries the program's name and nothing else; nothing is downloaded and nothing is installed. Settings › Nudges switches it off (`whaleshark set update.notice off`). On a server only the administrator's own `sudo whaleshark server status` asks.

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

## Reporting a problem or a flaw

A problem or a question: this repository's Issues page. `whaleshark doctor` ends with the same line and with output you can paste: it holds no secret. A security flaw: privately, as `SECURITY.md` says.

## Licence

MIT; see `LICENSE`.

## Credits

WhaleShark borrows ideas from [Orca](https://github.com/stablyai/orca) (MIT licence, copyright Lovecast Inc.): a run, its tasks and their attempts as separate things, with a result bound to its attempt; the worker's rules carried in its prompt; an inbox that replays until it is acknowledged; a question that blocks, survives a timeout and resumes by its id; never taking silence for failure; errors that name the next command, and one command table behind help and validation; a guide the tool prints, with a short pointer to it in the agent's own instruction file; trying a merge with git before making it; and deleting a branch only when it is proven merged. The `.worktreeinclude` file is a convention taken from there too. No code and no text was taken from Orca or from any other project: every line here was written for WhaleShark.

The engine is written from the published standards and manuals for terminals. Its shape, a resident program that owns the terminals and a window that is only a view, is common to programs of this kind. The first builds ran on herdr, a separate terminal program, while the engine was being written, and the wish for a tool this light came from using it; none of its code is used here, and it is not needed to run WhaleShark.
