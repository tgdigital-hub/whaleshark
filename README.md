# WhaleShark

WhaleShark (`whaleshark`) will be a small command-line tool for running a team of coding agents: you talk to one lead agent, it hands tasks to worker agents in their own terminal tabs, and the tool keeps the record of who is doing what, who is stuck and what is waiting to be checked.

The aim is Orca's way of orchestrating many agents, with herdr's lightness: one program file, no desktop app, and nothing else to install.

## Status

**Early: the foundation and the first layer. Nothing that runs an agent works yet.** This repository holds the shared contracts the rest is built on (the shapes of the files, the view model, the interfaces between the packages, the table of all 42 commands) and the first packages on top of them: the command line itself, the rules for every change to a run, the layer over the operating system, the adapter to herdr, the guides, the terminal layer, the build gate, the store, the view of a run and the first form of the two panes. `whaleshark help` lists every command and marks those not built yet.

## The terminal engine

WhaleShark is getting its own terminal engine, so that it needs nothing else installed. The engine is the part that keeps the agents' terminals alive in the background, draws the tabs and the panes, and reads what each program prints. **It is not built yet.**

The current code uses herdr, a separate program that you would install yourself, only as scaffolding while that engine is built. It calls herdr and contains none of its code. When the engine is in, herdr is taken out completely, and nothing is released before that.

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
- `internal/contract/testkit/`: the contracts of the tests: the fixture format and one fixture, the grammar of a scenario file, and how the fake herdr and the fake agents work together.
- every other folder under `internal/`: one package each. A package that is not written yet is a `commands.go` with an empty `Plug`.
- `build/ci/`: the gate, described below.
- `test/`: a stand-in for herdr, a scripted agent and the runner of scripted scenarios, for tests that need neither the real herdr nor a real agent.

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

The current code calls herdr, a separate program, as scaffolding until WhaleShark's own engine replaces it (see "The terminal engine" above). WhaleShark contains none of herdr's code.
