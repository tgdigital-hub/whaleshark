# WhaleShark

WhaleShark (`whaleshark`) will be a small command-line tool for running a team of coding agents: you talk to one lead agent, it hands tasks to worker agents in their own terminal tabs, and the tool keeps the record of who is doing what, who is stuck and what is waiting to be checked.

The aim is Orca's way of orchestrating many agents, with herdr's lightness: one program file, no desktop app, and nothing of ours running out of sight unless you switch it on.

## Status

**Early: the foundation and the first layer. Nothing that runs an agent works yet.** This repository holds the shared contracts the rest is built on (the shapes of the files, the view model, the interfaces between the packages, the table of all 40 commands) and the first packages on top of them: the command line itself, the rules for every change to a run, the layer over the operating system, the adapter to herdr, the guides, the terminal layer and the build gate. `whaleshark help` lists every command and marks those not built yet; `help`, `version` and `guide` work.

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
- `test/`: a stand-in for herdr and a scripted agent, for tests that need neither the real herdr nor a real agent.

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

It runs on herdr, a separate program that you install yourself. WhaleShark contains none of it.
