# WhaleShark on Windows: what is proven, and the checklist for a real machine

WhaleShark is built for Windows from the first day and proven there last. This page says what a hosted Windows machine has shown, what it found and what became of each finding, what no hosted machine can show, and gives the list a person walks on a real Windows PC before anyone may write "works on Windows".

## How to run the proof

The proof is the Go test package `test/platform/windows/`. It runs with every other test, on all three systems:

```
go test ./test/platform/windows/
go test -v ./test/platform/windows/        # every fact and every skip, in words
```

The hosted run of every push (`.github/workflows/gate.yml`) runs it on Windows Server 2025, macOS and Ubuntu. A test that asks something of Windows alone is skipped elsewhere and says `NOT PROVEN HERE`. On the hosted Windows machine nothing may skip quietly: where a shell or the browser a test needs is missing there, the test fails. Two tests change the machine they run on (the login's list of what starts at log-on; a package installed by winget), so they run on a hosted Windows machine only.

## What the hosted Windows machine proves

Each line is one test of `test/platform/windows/`; run one with `go test -v -run <name> ./test/platform/windows/`.

| What is proven | Test |
|---|---|
| **A file another program holds open.** PowerShell opens a run's record and keeps it open, with each of the four kinds of leave Windows knows. Where it gives no leave to replace the file (no leave at all; leave to read; to read and write), a command that changes the run waits two seconds and ends with exit code 3 and `state_busy`: every file of the run is byte for byte what it was, with no temporary file left, and the moment the other program lets go the same command goes through. With leave to replace, the command goes through under the open file | `TestARecordAnotherProgramHoldsOpen` |
| **Locking, by whole commands.** A real command from each of seven tabs at the same moment, five rounds, against one record: every command ends well and no tab's word is lost | `TestEveryTabAtOnce` |
| **File-change notices.** A real command's change of the record reaches a watcher within the second, five times out of five, by the system's own notice and not by the once-a-second look | `TestACommandsChangeIsTold` |
| **Worker commands through every shell.** `progress` with five texts (plain; every sign a shell could take for its own; accented letters and a dash; double quotes; double quotes with signs, backslashes and a backslash at the end) typed into `cmd`, Windows PowerShell, PowerShell 7 and git's bash, each spelled for that shell: the record holds the text as it was written, but for the double quotes in Windows PowerShell (finding 3). The same texts from `--file` arrive whole in all four. `mail` ends well in each | `TestWorkerCommandsThroughEveryShell` |
| **The line of the worker's prompt.** The prompt's own line for `progress`, one spelling, works in all four shells; so does a line the tool hands on with words of its own in it. As one side of a pipe it works in three, and not in `cmd` (finding 2) | `TestThePromptsOwnLineInEveryShell` |
| **Long paths.** A project with a file 322 characters deep: the task's own copy of the code, the worker's commit, the scan, the check, the merge and the landing all go through, once git is told `core.longpaths`. Without the setting git refuses the file, and `whaleshark doctor` has a row that says so with the command that sets it | `TestAProjectWithALongPath`, `TestGitAsItComesAndALongPath`, `TestDoctorSaysWhatGitDoesWithALongPath` |
| **The overlap scenarios** (two tasks on one file, both orders of the story) with git set as its Windows installer sets it, `core.autocrlf=true`: one clash is found and no more, the sync and the merge keep both tasks' lines, and what lands has Windows' line endings | `TestTheOverlapScenariosWithWindowsLineEndings` |
| **The keeper with real terminals.** `whaleshark engine run` in a login of the test's own: it answers on its socket file in the login's own folder; a tab gets its variables; a window of 120 by 40 gives the pane its size and is drawn into; a program started in the pane from a list of words prints through the pseudo-console; a second pane beside it makes the first narrower; closing the tab ends every program the pane started; a second keeper for the same login is refused; `engine stop` stops it; started again, a tab is back | `TestTheKeeperOnThisSystem` |
| **A keeper started at log-on, as far as a machine with nobody at it can show.** `whaleshark init --at-login` writes one line into the login's own list of what Windows starts at log-on; `doctor` says it is there; the line, started with a console of its own and no terminal as a log-on starts it, gives a keeper that passes the same questions; `init --remove --all` takes the line out again, and `doctor` says that too | `TestTheKeeperAtLogOn` |
| **The program in front in a pane.** A shell at its prompt, a program typed into it while it runs, the shell again after it | `TestTheProgramInFrontOnWindows` in `internal/pty/`; the `status` row of [the engine's table](engine-matrix.md) |
| **An agent's state, read from a real terminal.** A scripted agent with hooks in a pseudo-console goes through every state (ready, at work, at a question it asks, allowed, refused, ended), and the watcher of its pane names each from the agent's hooks and its screen and types nothing while a question is open; so do an agent with no hooks, one stopped at a trust question, one of a kind nothing is known of, and one that takes a paste in slowly or drops an Enter. These tests failed at their first run there for a haste of their own: on Windows an agent's screen says it is ready before its first hook, a program of its own, has named the session, and a test took the first state for the named one | the tests of `internal/watch/` |
| **The browser line.** The one command for a worker with no browser tool, as the prompt writes it on Windows, saves a picture of the worker's site before the line ends, in each of the four shells (one line for both PowerShells and git's bash, one for `cmd`); and again with Edge, as on a PC with no Chrome | `TestTheBrowserLine` |
| **An install from the winget files** as they would be submitted: `winget validate` reads the three files, `winget install --manifest` installs from them, the installed `whaleshark version` answers with the release's version, and `winget uninstall` takes it away | `TestInstallFromTheWingetFiles` |

Beside these, the rest of the suite runs on the same hosted machine and proves there what this folder does not repeat: twenty processes making two hundred changes each to one file and to one record (`go test -run TestTwentyProcesses ./internal/platform/ ./internal/store/`), a writer killed five hundred times, a thousand file changes each told, a thousand shells in a pseudo-console with no process left, and every scenario on the engine's stand-in.

## What the proof found, and what became of it

The proof found eight things that did not work on Windows. Each was mended in the program, or is said here as a limit that stays; each is held by a test, so the day one of them changes, a test says so.

1. **A record held with no leave at all was refused as "unreadable", at once. Mended.** A program that opens a run's record and lets nobody else even read it (a backup or an antivirus at its strictest) now meets the same two seconds of patience as one that only stands in the way of a replace, and a command that still cannot read the record ends with `state_busy`, the word for "held by another program", not the word a broken record gets. Nothing is damaged either way, and the next try works.
2. **The worker's prompt was written for PowerShell alone. Mended.** Nobody can know which shell an agent's tool types its commands into, so every line the tool hands an agent or a person to type is now written in the one spelling that `cmd`, Windows PowerShell, PowerShell 7 and git's bash all read alike: the program by its full path, bare and with forward slashes (`C:/tools/whaleshark.exe progress 40 "..."`); plain words bare; any other word between double quotes. Tried on the hosted machine, for the program's path alone: in double quotes it is refused by both PowerShells; with backslashes, single or doubled, it is lost on the way into git's bash; with forward slashes all four run it. **The limits that stay:** in `cmd`, and only there, such a line does not work as one side of a pipe (`... | more`): `cmd` then takes the path's first slash for a switch. And no such spelling exists for a program whose path holds a blank or a sign. The tool then writes the path by its short name (`C:/PROGRA~1/...`), which Windows keeps on most disks; where the disk keeps none, or where a word of the line holds a sign the four shells read differently (a double quote, `$`, `%`, `!`, a backtick), the line is written for PowerShell as before (`& '...' ...`). `whaleshark doctor` says so when the program's own path is one of those.
3. **Windows PowerShell (the one that comes with Windows) drops double quotes inside an argument. Stays; it is Windows' own.** `progress 40 'she said "done" twice'` arrives as `she said done twice`. PowerShell 7, `cmd` and bash hand the quotes on. Text with double quotes goes through `--file`, which works in every shell.
4. **The spelling the tool wrote for `cmd` broke an argument that holds a double quote. Mended.** A double quote is now written with a backslash before it, which is what the program reads, and from there to the end of that word every sign `cmd` could take for its own gets a caret, since `cmd` counts the quote as the end of its own quoting. What arrives equals what was meant for every sign the proof tried: `% & | < > ^ ! ; ( )`, backslashes (also before a quote and at the end), single and double quotes, accented letters.
5. **The prompt's browser line saved no picture on Windows, in any shell. Mended.** The browser is on no PATH there, no two shells spell a variable alike, and PowerShell and `cmd` do not wait for a program with a window of its own. So on Windows the prompt's line names no variable: the tool writes out the browser's file (Chrome in its usual folder; Edge where there is no Chrome), the profile folder, the evidence folder and the port, in the spelling of finding 2, and ends the line with `| sort`, which makes both PowerShells wait until the browser has ended and the picture is saved (git's bash waits anyway). `cmd` reads no such path before a pipe, so the prompt gives a second line for `cmd`, which waits by `start "" /wait`. They read like this:

   ```
   C:/PROGRA~1/Google/Chrome/APPLIC~1/chrome.exe --headless "--user-data-dir=D:\temp\whaleshark-browser-r1-T5.1" "--screenshot=C:\work\site\.whaleshark\runs\r1\attempts\T5.1\evidence\page.png" "--window-size=1280,900" http://localhost:21000/ | sort
   In cmd, this one in its place:
   start "" /wait "C:\PROGRA~1\Google\Chrome\APPLIC~1\chrome.exe" "--headless" "--user-data-dir=D:\temp\whaleshark-browser-r1-T5.1" "--screenshot=C:\work\site\.whaleshark\runs\r1\attempts\T5.1\evidence\page.png" "--window-size=1280,900" "http://localhost:21000/"
   ```
   (The prompt writes the loopback address in numbers where these lines say `localhost`.) On a PC with neither browser in its usual folder the prompt offers no such line, and a worker with no browser tool of its own cannot pass a browser check there.
6. **git on Windows refuses a path longer than 260 characters until it is told otherwise. A row in `doctor`.** With no setting, `git add` of the 322-character file fails. `whaleshark doctor` now says so on Windows, with the command that sets it (`git config --global core.longpaths true`), and says it is set once it is. The tool sets nothing in a person's git by itself.
7. **Nothing started the keeper at log-on on Windows. Built.** `whaleshark init --at-login` puts one line, `"<the program>" engine run`, under one name into the login's own list of programs Windows starts at log-on (the registry's Run key). Of the three ways Windows has, it is the lightest that needs no administrator and shows no prompt: a scheduled task "at log on" worked on the hosted machine, whose login is an administrator, and by Windows' own defaults as remembered (not tried here) it is refused to a login that is none unless written for that one login in a file of its own, does not start on battery and is ended after three days; a file in the Startup folder is a second program with a window of its own. The keeper lets go of the console window Windows opens for it. `doctor` has a row for the entry, and `init --remove --all` takes it out. **What stays:** unlike the entries of macOS and Linux, nothing starts a keeper again that has died; the next `whaleshark open` or the next log-on does. Without the entry, `whaleshark open` starts the keeper when none runs, and after a restart of the PC the first `whaleshark open` brings the tabs back.
8. **The scenario suite did not play the real keeper on Windows. It does now, in part.** The harness no longer skips it. What is played there on the real keeper: the keeper's own two scenario tests, and the first three scenarios of the suite. **What is not:** the rest of the suite, because both turns of every scenario take more than the ten minutes a test package has on the hosted Windows machine (nine minutes for ten of seventeen, with no failure among them); and the scenarios that begin from a made-up evening with twelve agents, whose agents all started in time on the real keeper in one hosted run and not in the next. Both are skipped there, out loud.

Found by the engine's own table and mended with these: **the keeper could not tell which program is in front in a pane**, so a shell's pane read `quiet`, an agent started by hand was not known, and no line was held back while another program was in front. Windows has no such thing to ask for, so the keeper reads it from the pane's own processes: the pane's first program, and from each shell on the way down the program that shell started last. **The limits that stay:** an agent started by hand is known by its program file's name, so one that runs inside another program's file (a script run by `node.exe`) is not known as an agent; and a program an agent itself starts is never taken to be in front of it.

Known before the proof and unchanged by it: on Windows no file is made private by the tool (a file has the permissions of the person's profile folder), "who else can write this" answers "cannot tell", and the keeper cannot ask who is on the other end of its socket file; the folder's permissions are the whole defence. On a PC used by one person this is what every program has. On a shared Windows machine it is unproven.

## What waits

- **A log-on.** A hosted machine has nobody who logs on: the entry's line is started by the test, with a console of its own. That Windows runs it at a real log-on, and that no window stays on the screen, is step 9 of the checklist.
- **winget's own catalogue.** The install is proven from the three files with the program fetched from the test's own machine, over https with a certificate the test makes and the machine is told to trust for that long; the checksum is the real one. Nothing was submitted to winget.

## What no hosted machine can show

A person, a real terminal window, a real agent, a real log-on, a second login, a sleeping laptop. That is the checklist.

## The checklist for a real Windows machine

For one person at one Windows 10 or 11 PC, about an hour. Tick each box or write beside it what happened instead; a box that cannot be ticked is a finding, not a failure of the person. "The window" is `whaleshark open` in Windows Terminal; "the lead tab" is the tab your lead agent runs in.

**Before you start.** Write down: the Windows version (`winver`), the terminal and its version, the PowerShell version (`$PSVersionTable.PSVersion`), `git --version`, the agent tool and its version, and which shell the agent's tool runs commands in (PowerShell, `cmd` or git's bash).

### 1. Getting the program

- [ ] Download `whaleshark-windows-amd64.exe` and the checksum list from the release; `Get-FileHash` gives the list's checksum for it.
- [ ] Put it in a folder on your PATH as `whaleshark.exe`. Write down what SmartScreen and the antivirus said when it first ran, in their own words, and what you had to click.
- [ ] `whaleshark version` answers in well under a second.
- [ ] `whaleshark doctor` prints one line per check and no line you do not understand. Write down every line that is not "ok".
- [ ] If winget carries it by then: `winget install WhaleShark.WhaleShark` in place of the download, then the same three checks.

### 2. A project

- [ ] In a git project of your own (not in OneDrive, Dropbox or Google Drive): type `whaleshark` alone. It sets the project up and opens the window.
- [ ] Do the same once in a folder inside OneDrive: it refuses, and says why.
- [ ] Close the window with its own key. `whaleshark engine status` still shows the keeper, with its tabs. `whaleshark open` shows them again, as you left them.

### 3. The window: tabs, keys, mouse

- [ ] The tab row, the dividing lines and the marks are drawn with whole characters: no boxes, no question marks, no line that is one cell off.
- [ ] Make the terminal window larger and smaller: every pane follows within a moment, and nothing is left drawn where it no longer is.
- [ ] Ctrl+Space opens the command key's list. Every action in it does what its name says.
- [ ] Click a tab: it comes to the front. Click into a pane: the keys go there. Drag a dividing line: it moves.
- [ ] Type in a pane's shell (PowerShell): letters, Backspace, the arrow keys, Tab, Ctrl+C on a running `ping -t localhost`, Ctrl+L. Each does what it does in a plain terminal.
- [ ] Type an accented letter and paste a line with an emoji: both show right and take the right number of cells.
- [ ] Select text in a pane with the mouse: the selection stays inside that pane's edges, and letting go copies it. Paste it into Notepad: it is the same text.
- [ ] With something selected, Ctrl+C copies and the selection goes. With nothing selected, Ctrl+C interrupts the running program.
- [ ] Ctrl+V pastes into the pane you are in, a long text of several lines too, as one paste.
- [ ] Run a full-screen program in a pane (`vim`, or `more` on a long file) and leave it: the pane is as it was before.

### 4. The fleet pane and the action pane

- [ ] In the lead tab, `whaleshark ui`: the fleet pane opens on the right and the action pane opens too.
- [ ] `whaleshark set fleet.width 40`, then drag the pane's edge with the mouse: both change its width.
- [ ] `whaleshark ui fleet off` and `on`, `whaleshark ui actions off` and `on`: each closes and opens its pane, and nothing else moves.
- [ ] `whaleshark set theme <name>` for each scheme `whaleshark help set` lists: the colours change at once in both panes, the context bar's too, and every scheme can be read.
- [ ] The last line of each pane says how old what it shows is, and the age stays small.

### 5. A run with real agents

- [ ] Start your agent tool in the lead tab and give it a small job of three tasks, one of which must ask you a question.
- [ ] Each worker gets a tab of its own, named in at most three words, and a card in the fleet pane with the same name, a bar and one line under it.
- [ ] A card changes within a moment of its worker changing: working, waiting for you, idle, done. It is never behind for more than a few seconds without saying so.
- [ ] The context bar of each agent moves as the agent works.
- [ ] The question shows in the action pane with a line to type in. Answer it there, by keyboard alone; the worker goes on. Do the next one by mouse alone.
- [ ] A yes-or-no item shows its choices as buttons; click one.
- [ ] Click a card: its tab comes to the front. Go back to the lead tab with one key.
- [ ] Watch one worker's own commands as it types them (`progress`, `ask`, `mail`, `report`). Write down the shell its tool used, and whether any of them failed. Write down exactly what it typed for one of them (finding 2 above).
- [ ] The lead agent accepts the work; `whaleshark land --local` puts it on your branch; `git log` shows it.
- [ ] `git status` in the project and in a task's own copy shows no file as changed that nobody changed (line endings).

### 6. The four buttons

Each is a button on the pane and a key; try both.

- [ ] **Stop all**, with one agent in the middle of a command and one at a question of its own: every agent stops at its next step and nothing new starts. **Resume**: each goes on.
- [ ] **Catch me up** after ten minutes away: it says what happened, and nothing it says is wrong.
- [ ] **Do not disturb** on: no nudge. Off: the nudges come back.
- [ ] **Mute**: the sound stops and the pop-up still shows.

### 7. Nudges

- [ ] With the window in the background, have a worker ask a question: a pop-up shows in the window's corner, and a sound is heard.
- [ ] `whaleshark set nudge.sound off`: the next one is silent. `whaleshark set nudge.popup off`: the next one shows nothing.

### 8. A browser check

- [ ] Add a task with `--browser-check` for a small page, and let a real agent do it. Its screenshot shows in `whaleshark show <task>`.
- [ ] Write down whether the agent used a browser tool of its own or the prompt's one command, and if the command, exactly what it typed (finding 5 above).
- [ ] Copy the line under "If you have no browser tool" from that task's prompt file and type it in a pane yourself, in PowerShell, and the `cmd` line under it in `cmd`, while the task's site runs: the picture is there when the prompt comes back.

### 9. Stops, restarts and log-on

- [ ] With three workers at work: `whaleshark engine stop`, then `whaleshark open`. The same tabs and panes are back, and each agent's conversation is resumed.
- [ ] End the keeper in Task Manager in the middle of a run, then `whaleshark open`: the same again, and the record of the run lost nothing (`whaleshark status`).
- [ ] Restart the PC in the middle of a run, log on, `whaleshark open`: the same again.
- [ ] `whaleshark init --at-login`, then `whaleshark doctor`: its row says the engine starts when you log on, and nothing asked for an administrator. Log off and on: `whaleshark engine status` shows a keeper you did not start, and `whaleshark open` shows your tabs. Write down whether a console window showed, and whether it stayed. Task Manager's list of start-up programs shows the entry.
- [ ] Close the laptop's lid for ten minutes in the middle of a run, open it: the window still answers and no worker was lost.

### 10. An upgrade under a running job

- [ ] With a worker at work and the lead agent in `whaleshark wait`: replace `whaleshark.exe` with a newer build. Write down whether Windows let you replace the file while it ran, and what you had to stop first.
- [ ] The lead agent's `wait` ends with the event `upgraded`, and its next `wait` goes on as before.
- [ ] The panes start themselves again with the new program, or say that they must be started again.

### 11. Other programs at the files

- [ ] Open a run's `state.json` (under `.whaleshark\runs\` in the project) in Notepad and leave it open: commands go on working.
- [ ] Open it in a program that locks what it opens (Excel will do) and run `whaleshark progress`-like work from a worker or `whaleshark answer` yourself: the command says `state_busy` after two seconds (finding 1) and names the file; close the program and the same command works. Nothing in `whaleshark status` is wrong afterwards.
- [ ] Leave the antivirus and the search indexer on for the whole hour, and write down every `state_busy` you saw that you did not cause.

### 12. Long paths and odd names

- [ ] In a project with a file deeper than 260 characters: without `core.longpaths`, `whaleshark doctor` has a row that says git refuses such a path, with its fix; start a task and write down what the tool says; then `git config --global core.longpaths true`, the row says so and the task starts.
- [ ] A project in a folder with a space and an accented letter in its name: a task runs from start to landing.
- [ ] A project on a second drive (`D:`): the same.

### 13. A second login (only if the PC has one)

- [ ] As another login of the same PC: `whaleshark engine status` shows nothing of yours, `whaleshark open` shows none of your tabs, and your project's `.whaleshark` folder and `%LOCALAPPDATA%\whaleshark` cannot be read. Write down exactly what could be read.

### 14. Taking it away

- [ ] `whaleshark init --remove --all` in the project, `whaleshark engine stop`, delete the program file: Task Manager's list of start-up programs no longer shows the entry, no process of ours is left, and `%APPDATA%\whaleshark` and `%LOCALAPPDATA%\whaleshark` are the only folders left; delete them.

**When you are done:** the list with its notes goes to whoever integrates the phase. Only when every box is ticked, or its finding is fixed or written down as a known limit, may the documentation say that WhaleShark works on Windows.
