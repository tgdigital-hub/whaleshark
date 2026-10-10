# Writing scenarios

A scenario is a text file (`*.scn`) that plays one flow of the tool from end to end: a made-up project in a home folder of its own, a stand-in for the terminals, fake agents in its tabs, and the real `whaleshark` program started as each kind of caller. No keeper of the person's own and no real agent is touched, and nothing costs a token.

## Running them

A package of scenarios needs two things, and then one test per file:

```go
func TestMain(m *testing.M) { scenario.Main(m) }

func TestAskAndDie(t *testing.T) {
	scenario.Run(t, "testdata/ask-and-die.scn", nil)
}
```

`Main` builds the real program and the fake agent once and puts them first on the PATH. `Run` fails the test at the first line that does not go as written and says which line. Its last argument is the program of the panes; a scenario that opens no pane passes nil. Scenarios of one package run one after the other, not side by side: each gets a home folder through the environment of the test.

## The file

One step per line. A line that starts with `#` is a comment. The first word says what the line is; `internal/contract/testkit` holds the list, and a word that is not on it fails the test.

```
fixture evening-2114
agent T1.1: progress 85 "nearly there" | write-result | report done "ok"

orch: wait                       -> expect done T1
orch: wait --ack last --timeout 0
orch: accept T1
human: answer n4 approve
assert: T1 status done; attempts T1.1 accepted; no event lost
```

**Where it starts.** `fixture <name>` starts from a prepared run: a fixture of the testkit is created as a run through the program's own store, which also sets the current pointer, so the files are the ones a command would have written; `ui.json` and the context files are written beside it, every live attempt gets a token file whose hash the record holds, and the fixture's picture is handed to the terminals together with the two panes `ui.json` names. Without that line the project is empty, with one tab for the lead agent, and the scenario makes its run itself (`orch: init`, `orch: run new "..."`).

**Fake agents.** `agent <attempt>: <step> | <step>` is what the fake agent of that attempt plays once `start` opens its tab. For an attempt the fixture already shows in a tab, the agent is started at once, in a tab of its own that the record then names. The steps are the testkit's `AgentSteps`. Those that are commands (`progress`, `ask`, `mail`, `report`) run the real program from the tab, so the worker's side is tested too.

**A real repository.** `git` makes the project folder a git repository, with what the scenario has written so far committed on `main`. From then on a task gets a copy of the code of its own, as in any project with git, and a fake agent's `edit <file>` and `commit` work in that copy; `merge <branch>` brings a branch into it and, where the two clash, keeps the lines of both, as a worker does with a sync brief. Without the line the project has no git and every task works in the one folder.

**Approval.** `approve` is the person running `trust` and typing yes: everything the project's own files would run or reach, as it stands at that line, is approved. A scenario that sets a held key of `whaleshark.toml`, or runs a team that comes with the project, needs it first; a file changed afterwards is held again.

**Files.** `file brief.md: Target | Change | Constraints | Ownership | Acceptance` writes a file into the project folder, one line for each part between the bars: a task needs a brief with those five headings.

**Commands.** Everything after the first word is a real command line, without the program's name. Quotes, double or single, keep words together.

| Line | Run as |
|---|---|
| `orch: <command>` | the lead agent, from its pane |
| `task <id> "<title>" [flags]` | short for `orch: task add ...` |
| `human: <command>` | the person: `--human` is added, from a pane no run records |
| `human: page: <command>` | a button of the page: its mark and no pane at all |
| `as-worker <attempt>: <command>` | from that attempt's tab, with the tab's variables |
| `as-unbound: <command>` | from a pane no run is bound to |

A command must end with exit code 0. `-> expect` at the end of the line says otherwise, or more: `-> expect exit 5 not_bound` wants that exit code, and every word after it must be somewhere in what the command printed. `--ack last` stands for the delivery id the last `wait` printed. A line that ends in `&` is left running, which is how a `wait` or an `ask` is made to block; `kill-wait` ends every waiting `wait`, and whatever still runs is ended with the scenario. A command that is not left running and has not ended after twenty seconds fails the test.

**Time.** The clock stands still at the fixture's moment, or at the moment the scenario began. `clock +30s` moves it for every command and pane, and for every fake agent: the double hands an agent the clock and the notices switch of the test, as the keeper hands an agent its own surroundings.

**The terminals.** `term-event <kind> <task>` changes the terminals' picture and tells the change; `term-drop` changes the picture and, on the double, tells nobody. The kind is `working`, `idle`, `blocked`, `unknown`, `gone` (the agent left its pane), `closed` (the pane was closed) or `focused`. In the place of a task, an attempt or `lead` may stand. `restart-terminals` does what a restart of the keeper does: the same panes with their variables, a new terminal in each. `kill-orchestrator` ends the lead agent's commands and takes its agent out of its pane.

**Panes.** `pane fleet: 100x30` starts that pane on a pretended terminal of that size, through the same terminal layer as a real one; `pane actions: 60x30` the other. The steps that follow act on the pane named last; `pane fleet:` with no size turns them back to one that is open, and a size starts the pane again. `click <text>` clicks the first cell of that text, `key <name>` presses one key and `type "<text>"` types, each after the half second a person leaves (a pane takes keys that follow faster for typing meant elsewhere, and refuses a click on a row that has just moved); a key is `enter`, `esc`, `tab`, `up`, `ctrl+c` or a letter. `expect-row <text>` waits up to twenty seconds for some row to show the text (what a button started may stand in line behind a command that takes its time), `expect-last-line <text>` for the last line to. `notices off` makes file changes arrive without a notice in every pane and command started after it.

**Facts at the end.** `assert:` lines are checked once every step has run, against the record on disk. An `await:` line takes the same facts and waits where it stands, up to twenty seconds, until the record says so: a fake agent reports when it gets there, not when the next line runs. Facts are separated by `;`:

- `<id> <field> <value> ...` for a task, an attempt, a question or an item, with the fields named as in `state.json`: `T2 failures 1 status ready`; a field inside a field is named with dots: `T2 worktree.setup ok`;
- `attempts T2.1 exited, T2.2 reported`: the state of each;
- `no event lost`, `no event twice`: every number given out since the scenario began is in the inbox, in the history or on a message to a worker, and none is there twice.

## A prepared run without a file

A test that only needs "an attempt that is already working" takes the fixture, changes what it must, and has a project to run commands in:

```go
f, _ := testkit.Load(testkit.Evening)
f.State.Tasks["T7"].Failures = 2
p := scenario.Prepare(t, f, nil)
out, err := p.Command("T2.1", "progress", "50", "half").CombinedOutput()
```

`Command` takes `scenario.Orch`, `Human`, `Page`, `Unbound` or an attempt's id. `p.Record()` reads the record back through the store, `p.Clock(d)` moves the clock, `p.Double` is the double, and `p.Kit` holds the real platform and the real store beside it. A test that opens the real panes plugs the view builder into that kit and hands `Run` a function that calls `panes.Run`; `testdata/join.scn` is the example.

## The two backends

`Run` and `Prepare` put the engine's double behind the terminals. `RunOn` and `PrepareOn` take a backend first:

| Backend | What stands behind the terminals | How a command reaches it |
|---|---|---|
| `scenario.Double` | the engine's double (`test/fakeengine`): tabs, panes and fake agents in memory, answering the keeper's own calls; `p.Double` is the double | the real adapter, over a socket file of the test's own |
| `scenario.Keeper` | the real keeper: `whaleshark engine run` in the project's own login, with real terminals, stopped with the test | the real adapter, over a socket file of the test's own |

A scenario file needs no change: the same lines are played on each. `p.Kit.Terms` is the terminals on both, `p.Env(pane)` is what a program started in a pane has in its environment to reach them, and `p.Log` is what the run came to in words no backend changes: every command with its exit code, then every task, attempt and question with its state and the events by kind. `TestEveryScenarioOnEveryBackend` plays every `*.scn` of the tree on both and holds the keeper's log against the double's; `TestTheDoubleAnswersAsTheKeeper` makes one round of calls on the real keeper and on the double and compares every answer and event.

The real keeper is changed by nothing but its own file and its own calls, so what the double is simply told comes about there as it does for real. A `fixture` is written as the keeper's layout file before it starts, each pane under the fixture's id, and each agent is started in its pane by `agent-start`: the fake agent, which stands on the `PATH` under the names of the agent kinds, says through the `hook` call that it is in the fixture's state. An `agent` line's script is a file the fake agent finds by its attempt. `term-event` and `term-drop` are the event of an agent's own hooks, a shell run in the pane, or the call that closes or focuses; the keeper loses no event, so the two are one. `restart-terminals` stops the keeper and starts it again, and it brings everything back by itself. A pane with no agent reads `idle` or `working` on the keeper, as a shell at its prompt or at work does.

## What is not played

The double keeps no pane sizes and no layout: a pane's neighbour is the pane split off on that side. It starts nothing for a program run in a pane, and tells its changes at once and in order. The project folder is a git repository only where a scenario says `git`. The token of a fixture's live attempt is `token-of-<attempt>`.
