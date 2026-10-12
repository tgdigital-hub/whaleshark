# How WhaleShark is put together

A short note for someone about to read the code. The README says what has and has not been tried.

## The idea

One lead agent talks to you. It plans the work as tasks, each with a brief and a check, and hands each to a worker: another agent, in a terminal tab of its own. WhaleShark is the record between them. A worker reaches the lead agent only through four commands (report, progress, ask, mail), and the lead agent sees a worker only through the tool. Nothing is finished until `accept` has run the task's check.

The lead agent is called the orchestrator, and its tab is named so. With Fleet Mode on there is one level more: the orchestrator's tasks are groups of work, each placed in a fleet; a fleet is a job of its own inside the orchestrator's, led by a fleet leader that is the lead agent of that job and a worker of the orchestrator's. So a leader plans, starts and checks its workers with the same commands and the same record, one level down, and a question climbs one level at a time. A fleet ends with its work unless the person saved it by name with what it works on (`run fleet`); a saved fleet is a line in the project's own folder and nothing else until work is placed in it. Fleets are not walled apart: what keeps two of them from undoing each other is the scan for work on the same file, the sync that follows it, and the check on the merged work.

Every agent tool is a description: the command that starts it, how it names a model, what its screen shows at rest and at work, whose hooks it reads, and the file it reads when it starts. Into that file `init` writes a short block that says how the team is built and which guide an agent reads for its part.

## One program, one table

Everything is one program file, `whaleshark`. Its 42 commands are rows of one table in `internal/contract/`: the name, who may call it, its flags, its exit codes, an example. Help, validation and the caller rules are all read from that table, so they cannot drift apart. An error names the command to run next.

`cmd/whaleshark` builds one kit (the system, the store, the rules, the terminals) and hands it to every package under `internal/`; each package plugs its commands into the table. Packages share only what is in `contract`, and the gate checks which package may import which.

## The record

A run, its tasks and each task's attempts are separate things, kept as files in the project's `.whaleshark` folder, private to the login. Every change is made under a file lock and written by replacing the file, never in place. One package, `rules`, decides whether a change is allowed in the run's current state; the commands ask it inside the lock.

A result belongs to an attempt, not to a task, so a late report from a stopped attempt changes nothing. What the lead agent must hear goes into an inbox that hands out the oldest batch again and again until it is acknowledged; a lead agent that lost its thread loses no event. A worker's question blocks, survives a timeout, and is resumed by its id. Silence is never taken for failure: an agent is working, waiting, quiet or proven gone, and only the last frees its task.

Nothing depends on a program that stays running. Any command can sweep the record and settle what has changed; the panes and the page only ask more often.

## The code's copies

In a git project each task gets a worktree on a branch of its own, made from the work collected so far. `accept` merges a task into one branch for the run and runs the check on the merged result; `land` brings that branch to yours, by a pull request or a local merge, and is yours to type. `overlap` tries merges without making them and says which tasks would conflict; the worker mends a conflict in its own copy, never the tool.

## The engine

The terminal engine is WhaleShark's own, written from the published standards for terminals.

- **The keeper** is the one resident program of a login. It owns every pane's pseudo-terminal, reads what each program prints into a grid of cells, and keeps the layout of tabs and panes. It listens on a socket file only the login can open.
- **The window** (`whaleshark open`) is only a view: it joins the keeper, draws what the keeper composes from cells, and sends keys and mouse events back. Closing it stops nothing. Nothing a program prints reaches your terminal as it came.
- **The state of an agent** (working, at a prompt, idle, gone) comes from the agent's own hooks where it has them, and from what its screen shows where it has not.
- After a stop or a restart the keeper brings back the same tabs and panes and resumes each agent's conversation where the agent tool can.

## What you look at

The fleet pane and the action pane are ordinary programs in panes of the keeper. The page (`whaleshark dash`) is a small web server of the login's own on a socket file. All three draw from one view model, and every button or key runs one row of one action table, each row an ordinary command: a button can do nothing a typed command cannot, and several typed commands have no button on purpose.

## Staying small

The gate (`go run ./build/ci`) fails the build when a limit in `build/ci/limits.txt` is passed: a line budget for every folder and 41,500 lines for all of them, four modules, 15 MB a program file, 42 commands, and 30 ms for the commands an agent's harness runs at every step. A figure changes only in that file, with its reason beside it.

Other rules of the same kind: no C compiler; no code loaded at run time; no shell string for a child program except the ones a person approved or the lead agent gave as a check; what is typed into an agent's tab is a fixed pointer to a file, never the text itself; every file carries a version, and a program refuses to write a record newer than itself.

## Three systems

The differences between macOS, Linux and Windows live in one package, `platform`: folders, locks, replacing a file, file-change notices, the shell. The gate allows the system's name to be read there and nowhere else. The program builds for all three; where it has run is in the README.
