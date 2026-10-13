# The engine's matrix

Every command of WhaleShark reaches the panes through one interface, `contract.Terminals`
(`internal/contract/ports.go`), which the engine's keeper answers. This table says, for each
call of that interface and each kind of event it sends, whether it works on each system.

The table is not written by hand and hoped true. The test in `test/platform/engine/` starts a
real keeper in a pretended login, with real terminals and a real window, makes every call, and
fails where a cell here says anything but what it found. It runs with every other test on a
hosted macOS, Ubuntu and Windows machine at each push. A call or a kind of event that is added
to the interface fails the test until it has a row.

    go test ./test/platform/engine/

## The table

`yes`: the call was answered and what it should bring about was seen. `no: not seen`: it was
answered, and what it should bring about did not come within ten seconds. `no:` with another
word: it was refused, under that name. The `status` row holds the word the pane read.

| Call or event | macOS | Linux | Windows |
|---|---|---|---|
| `Version` | yes | yes | yes |
| `Snapshot` | yes | yes | yes |
| `Events` | yes | yes | yes |
| `TabCreate` | yes | yes | yes |
| `TabRename` | yes | yes | yes |
| `TabFocus` | yes | yes | yes |
| `TabClose` | yes | yes | yes |
| `AgentStart` | yes | yes | yes |
| `Prompt` | yes | yes | yes |
| `Point` | yes | yes | yes |
| `Run` | yes | yes | yes |
| `Screen` | yes | yes | yes |
| `Split` | yes | yes | yes |
| `Swap` | yes | yes | yes |
| `Resize` | yes | yes | yes |
| `PaneClose` | yes | yes | yes |
| `Size` | yes | yes | yes |
| `PaneFocus` | yes | yes | yes |
| `Notify` | yes | yes | yes |
| `SetKeys` | yes | yes | yes |
| `Overlay` | yes | yes | yes |
| event `pane-opened` | yes | yes | yes |
| event `pane-closed` | yes | yes | yes |
| event `state` | yes | yes | yes |
| event `focus` | yes | yes | yes |
| event `renamed` | yes | yes | yes |
| event `folder` | yes | yes | yes |
| event `session` | yes | yes | yes |
| event `tab-closed` | yes | yes | yes |
| event `window` | yes | yes | yes |
| event `space` | yes | yes | yes |
| event `menu` | yes | yes | yes |
| `status` of a pane with a shell at its prompt | idle | idle | idle |
| `open`, the window | yes | yes | yes |
| `hook state`, an agent's own word | yes | yes | yes |
| all of the above again over `ssh` | yes | yes | not tried |

## What a row asks

One keeper, two tabs, an agent, a window, in this order.

| Row | What is done, and what must be seen |
|---|---|
| `Version` | the keeper says a version |
| `Events` | the picture, then every change; applying the changes to the first picture gives the last one exactly; none is missing or out of order; the reading ends when the keeper stops |
| `TabCreate` | a tab with a shell in a folder, and a second one; the pane is in the picture, in that folder |
| `Snapshot` | the picture holds the new pane under its tab's name |
| `Split` | a pane to the right by a share, and one above it five cells high |
| `Size` | a pane's size in cells |
| `TabRename` | every pane of the tab carries the new name |
| `TabFocus` | the tab's pane has the keys |
| `PaneFocus` | the keys to a pane by its name, and to its neighbour on the right |
| `Swap`, `Resize` | the call is answered |
| `Notify` | with no window, the answer is "no window" |
| `open` | the real window, in a terminal the engine itself made: it attaches, the same notice is then "shown", the panes take the window's size, and the pane five cells high is five cells high; the command key and a full stop are then typed into it, for the event `menu` |
| `AgentStart` | the agent's program starts in the pane in the shell's place, and the pane is known as that agent's |
| `Screen` | the pane's text holds what the agent printed |
| `Prompt` | the prompt is typed and the agent goes to work on it |
| `Point` | one of the fixed lines is typed at the resting agent, and the agent shows that it got it |
| `hook state` | what the agent's hooks say of its session and of its folder is taken, and each comes as an event of its own kind |
| `Run` | a program is run in a pane from a list of arguments, and the pane closes when it ends |
| `Overlay` | a program runs in a pane over the tab |
| `PaneClose` | that pane is gone |
| `SetKeys` | the shortcut is in the settings file |
| `TabClose` | the tab's panes are gone |
| `status` | the word a pane reads once its shell is at its prompt, waited for ten seconds |
| an event | at least one of its kind came in the stream |

## Over ssh

The test starts an `sshd` of its own on the loopback, as the login that runs the test, with a
host key and a login key it has just made; it reads and writes nothing of the login's own ssh
settings, keys or known hosts. Through it, it runs the whole column a second time: the program
that makes the calls, the keeper and every pane are then started by `sshd`, with none of the
near side's surroundings and with no terminal, as a command typed after `ssh server` is.

## What is missing

- **The events `space` and `menu` were seen on macOS.** On Linux and Windows it is the same
  code, to be seen at the next run there. The `menu` event is asked for with the command key
  then a full stop, typed into the window; a right press on a tab's name is held by the
  keeper's own tests.
- **On Windows the program in front in a pane is worked out, not asked for**: the system has
  no such thing. The keeper reads it from the pane's own processes: the pane's first program,
  and from each shell on the way down the program that shell started last. Seen here: a pane
  with a shell at its prompt reads `idle`. By the engine's own tests on the hosted machine, a
  program typed into the shell is in front while it runs. By the code and not tried with a
  real agent: an agent a person starts by hand is known by its program file's name, so one
  that runs inside another program's file (a script run by `node.exe`) is not known as an
  agent; and a program an agent itself starts is never taken to be in front of it, so a line
  for that agent is not held back for it.
- **Over ssh on Windows** was not tried: the test starts no `sshd` there.
- **Both ends of the ssh are one machine.** A second computer, a slow or broken link, a
  connection that drops and comes back, and the window drawn through ssh's own terminal
  (`ssh -t server whaleshark open`) are not in this test.
- **The window is real and nobody looks at it.** It runs in a terminal of the engine's own
  making and what it draws is thrown away. A terminal program a person uses, keys, the mouse,
  copy and paste are not calls of this interface and are not here.
- **The agent is a scripted stand-in** under an agent kind's name. No real agent program is
  started.
- **Only what goes well.** Each row is the call doing its work once. The refusals (a pane that
  is not there, an agent at a question, a prompt that does not start) are tested with the
  keeper and in the scenario suite.
- **Windows is the hosted machine's Windows Server.** No row has run on a Windows PC with a
  person at it.
- **A pop-up's look and a sound** are not checked: only what `Notify` answers.
