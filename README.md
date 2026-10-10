# WhaleShark

WhaleShark (`whaleshark`) will be a small command-line tool for running a team of coding agents: you talk to one lead agent, it hands tasks to worker agents in their own terminal tabs, and the tool keeps the record of who is doing what, who is stuck and what is waiting to be checked.

The aim is Orca's way of orchestrating many agents in something light: one program file, no desktop app, and nothing else to install.

## Status

**Early, and not released.** What is built: the command line with its 42 commands in one table, the record of a run and the rules for every change to it, the terminal engine, starting workers in tabs and waiting for what they report, questions and answers, the two panes a person watches (the fleet and the action pane), Stop all and Resume, the nudge, and the gate that holds the tree to its limits. In a project with git each task now gets a copy of the code of its own on its own branch; finished work is checked and collected on one branch for the run, one task at a time or several together, and brought to your own branch with one command that only you may type. The tool finds tasks that change the same files, says which would conflict, and hands a worker what to merge in; a project's own settings that can run something count only once you have approved them, and what you approved is kept in your login's own folder: an approval, or a whole `.whaleshark` folder, that comes along with a repository is not used; a plan can be saved as a team and run again; `whaleshark doctor` checks an installation line by line.

Built since, for a rented server and for a phone, and so far tried by tests and on one Mac alone:

- **The page in your browser.** `whaleshark dash` is a small server of your own login that listens on a socket file and on nothing another machine can reach; `whaleshark connect` brings it to your own computer through your SSH connection. It shows the same items, cards and buttons as the panes, and the screenshots of a browser check.
- **The same page on a phone**, by one of two roads the server's administrator chooses: a private network (Tailscale), or a public name behind Cloudflare's login, where the page checks Cloudflare's signed pass on every request itself. A phone is paired once, by a number you approve on the page, in the action pane, or at a terminal with `whaleshark dash phone approve`. A paired phone can be nudged when you are needed.
- **A browser check for a task.** `whaleshark task add ... --browser-check` marks work that must be looked at in a browser. The tool starts no browser: the worker uses its own, saves screenshots into a folder of its attempt, and a report without one is refused; `whaleshark show` lists them, and the task is not accepted without them.
- **A server for several people.** `whaleshark server` sets a Linux machine up so that each person is a login of their own, with their own block of ports and a cap on how many agents they run; `whaleshark doctor --server` checks such a machine line by line. An install script is written and waits for the first release.

What is true of it today, plainly:

- The parts for one computer have been run from end to end on macOS only, by scripted scenarios and with real Claude Code agents. In the last such run 26 workers were started and all 26 started, and twenty working at once all reported, were accepted and were landed. Those tasks were trivial ones made for the try. Nobody has yet used it for real work; a person has had one first look at the engine in a real terminal window, and nothing more.
- **Nothing of the server, the page, the phone or the tunnel has met a real server, a real phone, a Tailscale network or a Cloudflare account.** Those parts are written from their makers' manuals and held by tests on one Mac, with a stand-in for every program of a server. The page has been opened in one real browser, Chrome without a window on that Mac, through `whaleshark connect` and an SSH server on the same machine; no other browser has seen it, no phone has, and no nudge has reached a real phone. Expect them to need mending at their first real use.
- A browser check has been played with a scripted agent only. The one command the worker's prompt names for taking a screenshot without a browser tool has been run on a Mac, by hand, and nowhere else; no real agent has yet been told to do a browser check.
- The first worker started in a project you have never opened in Claude Code stops at Claude Code's own question whether you trust the folder; the tool tells you so and answers nothing, and after your one yes in the project's folder it does not come again.
- Claude Code is the one agent it knows well: a second kind is built from its maker's manual and has never been run.

What is not built: a release, so there is nothing yet for the install script to fetch; packages for a package manager; the proof on real Linux and Windows machines. `whaleshark help` lists every command.

## The terminal engine

WhaleShark has its own terminal engine and needs no other program installed. The engine is the part that keeps the agents' terminals alive in the background, draws the tabs and the panes, reads what each program prints, and knows whether an agent is working, waiting for you or idle. It is in this repository, written for WhaleShark from the published standards for terminals, and every command of the tool runs on it.

- `whaleshark open` shows your tabs and panes in the terminal you are in. It starts the background program, called the keeper, when none is running. Closing the window stops nothing: the agents carry on, and the next `whaleshark open` shows them again.
- If the keeper stops or the machine restarts, the next start brings back the same tabs and panes, and resumes the conversation of each Claude Code agent the tool started.
- Text you select with the mouse is copied when you let go of the button. Paste is your terminal's own key.

What is true of it today, plainly: it has been run on macOS only, by its tests and through a scripted terminal with a real Claude Code agent. Nobody has yet worked a day in it in a real terminal window, so expect rough edges. It builds for Linux and Windows, and nothing of it has been tried there yet. Claude Code is the one agent whose state it knows exactly; any other program in a pane is shown as busy or quiet.

The price is size: the limit on everything shipped moved from 28,000 lines to 40,000 to make room for the engine.

## Building

It needs Go 1.27 or newer and nothing else.

```
go build -trimpath -ldflags="-s -w" -o bin/whaleshark ./cmd/whaleshark
go test ./...
```

## Layout

- `cmd/whaleshark/`: the entry point. It hands one kit to every package and runs the command.
- `internal/contract/`: everything that is shared. Only a contracts change edits it.
- `internal/contract/testkit/`: the contracts of the tests: the fixture format and one fixture, the grammar of a scenario file, and how the engine's test double and the fake agents work together.
- every other folder under `internal/`: one package each. A package that is not written yet is a `commands.go` with an empty `Plug`.
- `build/ci/`: the gate, described below.
- `test/`: a stand-in for the engine, a scripted agent and the runner of scripted scenarios, which plays every scenario on the stand-in and on the real engine; the recordings the screen reader is tested with.

## Checks

One program holds the tree to its limits and runs every check: `go run ./build/ci`. It builds for the five systems, runs the tests, counts lines per folder, modules, commands and the size of the file against `build/ci/limits.txt`, checks which package may reach which, times the commands that must be fast, and looks for anything private in what would be published. `build/ci/README.md` says what each step does.

Five outside tools do the security checks. None is part of the program and the gate installs none; one that is missing is skipped on your own machine, said out loud, and fails a hosted run.

| Tool | For |
|---|---|
| [staticcheck](https://staticcheck.dev) | a stricter static analyser than `go vet` |
| [gosec](https://github.com/securego/gosec) | shell strings, weak file permissions, unchecked paths |
| [govulncheck](https://go.dev/doc/security/vuln/) | our modules against the public list of known flaws |
| [gitleaks](https://github.com/gitleaks/gitleaks) | secrets in the files and in the history |
| [shellcheck](https://www.shellcheck.net) | every shell script in the repository |

The hosted runs in `.github/workflows/` use the same program. They take no step from any other project: the commit is fetched with plain git and built with the machine's own Go.

## Dependencies

At most four modules, and no C compiler. A new one needs a written reason here.

| Module | Why |
|---|---|
| `github.com/BurntSushi/toml` | reads `whaleshark.toml` and a person's own settings file |
| `golang.org/x/sys` | file locks, file-change notices, the console on Windows |
| `golang.org/x/term` | raw mode and the size of the terminal, for the panes |
| `github.com/rivo/uniseg` | how many cells a piece of text takes on the screen |

## Licence

MIT; see `LICENSE`.

## Credits

WhaleShark borrows ideas from [Orca](https://github.com/stablyai/orca) (MIT licence, copyright Lovecast Inc.): a run, its tasks and their attempts as separate things, an inbox that replays until it is acknowledged, a question that blocks and survives a timeout, and never taking silence for failure. No code or text was taken from Orca or from any other project: every line here was written for WhaleShark.

The first builds of WhaleShark ran on herdr, a separate terminal program, while this engine was being written, and the wish for a tool this light came from using it; none of its code is used here, and it is not needed to run WhaleShark.
