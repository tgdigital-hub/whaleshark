# WhaleShark

WhaleShark (`whaleshark`) will be a small command-line tool for running a team of coding agents: you talk to one lead agent, it hands tasks to worker agents in their own terminal tabs, and the tool keeps the record of who is doing what, who is stuck and what is waiting to be checked.

The aim is Orca's way of orchestrating many agents, with herdr's lightness: one program file, no desktop app, and nothing of ours running out of sight unless you switch it on.

## Status

**The foundation only. No command works yet.** This repository holds the shared contracts the rest is built on: the shapes of the files, the view model, the interfaces between the packages, and the table of all 40 commands. `whaleshark help` lists them, each marked "not built yet".

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
- every other folder under `internal/`: one package each, today a `commands.go` with an empty `Plug`, replaced by the package when it is written.

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
