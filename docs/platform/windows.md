# WhaleShark on Windows: what is proven, and the checklist for a real machine

WhaleShark is built for Windows from the first day and proven there last. This page says what a hosted Windows machine has shown, what it found that does not work yet, what no hosted machine can show, and gives the list a person walks on a real Windows PC before anyone may write "works on Windows".

## How to run the proof

The proof is the Go test package `test/platform/windows/`. It runs with every other test, on all three systems:

```
go test ./test/platform/windows/
go test -v ./test/platform/windows/        # every fact and every skip, in words
```

The hosted run of every push (`.github/workflows/gate.yml`) runs it on Windows Server 2025, macOS and Ubuntu. A test that asks something of Windows alone is skipped elsewhere and says `NOT PROVEN HERE`. On the hosted Windows machine nothing may skip quietly: where a shell or the browser a test needs is missing there, the test fails. Two tests change the machine they run on (a scheduled task; a package installed by winget), so they run on a hosted Windows machine only.

## What the hosted Windows machine proves

Each line is one test of `test/platform/windows/`; run one with `go test -v -run <name> ./test/platform/windows/`.

| What is proven | Test |
|---|---|
| **A file another program holds open.** PowerShell opens a run's record and keeps it open, with each of the four kinds of leave Windows knows. Without leave to replace the file (none at all; to read; to read and write) a command that changes the run waits two seconds, ends with exit code 3 and `state_busy`, and every file of the run is byte for byte what it was, with no temporary file left; the moment the other program lets go the same command goes through. With leave to replace, the command goes through under the open file | `TestARecordAnotherProgramHoldsOpen` |
| **Locking, by whole commands.** A real command from each of seven tabs at the same moment, five rounds, against one record: every command ends well and no tab's word is lost | `TestEveryTabAtOnce` |
| **File-change notices.** A real command's change of the record reaches a watcher within the second, five times out of five, by the system's own notice and not by the once-a-second look | `TestACommandsChangeIsTold` |
| **Worker commands through every shell.** `progress` with four texts (plain; every sign a shell could take for its own; accented letters and a dash; double quotes) typed into `cmd`, Windows PowerShell, PowerShell 7 and git's bash, each spelled for that shell: the record holds the text as it was written, but for the double quotes in two of the shells (findings 2 and 3). The same texts from `--file` arrive whole in all four. `mail` ends well in each | `TestWorkerCommandsThroughEveryShell` |
| **The line of the worker's prompt.** The prompt's own line for `progress` works in both PowerShells | `TestThePromptsOwnLineInEveryShell` |
| **Long paths.** A project with a file 322 characters deep: the task's own copy of the code, the worker's commit, the scan, the check, the merge and the landing all go through, once git is told `core.longpaths` | `TestAProjectWithALongPath` |
| **The overlap scenarios** (two tasks on one file, both orders of the story) with git set as its Windows installer sets it, `core.autocrlf=true`: one clash is found and no more, the sync and the merge keep both tasks' lines, and what lands has Windows' line endings | `TestTheOverlapScenariosWithWindowsLineEndings` |
| **The keeper with real terminals.** `whaleshark engine run` in a login of the test's own: it answers on its socket file in the login's own folder; a tab gets its variables; a window of 120 by 40 gives the pane its size and is drawn into; a program started in the pane from a list of words prints through the pseudo-console; a second pane beside it makes the first narrower; closing the tab ends every program the pane started; a second keeper for the same login is refused; `engine stop` stops it; started again, a tab is back | `TestTheKeeperOnThisSystem` |
| **A keeper started by a log-on task.** Windows' scheduler starts the keeper from a task set to run at log-on, and that keeper passes the same questions | `TestTheKeeperStartedByALogOnTask` |
| **The browser line.** The one command for a worker with no browser tool, as it must be spelled for each shell, saves a picture of the worker's site before the line ends | `TestTheBrowserLine` |
| **An install from the winget files** as they would be submitted | `TestInstallFromTheWingetFiles` (waits for the release's files; see below) |

Beside these, the rest of the suite runs on the same hosted machine and proves there what this folder does not repeat: twenty processes making two hundred changes each to one file and to one record (`go test -run TestTwentyProcesses ./internal/platform/ ./internal/store/`), a writer killed five hundred times, a thousand file changes each told, a thousand shells in a pseudo-console with no process left, and every scenario on the engine's stand-in.

## What the proof found

These are true of Windows today. Each is held by a test, so the day one of them changes, a test says so.

1. **The worker's prompt is written for PowerShell alone.** Its command lines begin with PowerShell's call sign (`& 'C:\...\whaleshark.exe' progress ...`). `cmd` and git's bash refuse such a line. An agent whose own tool runs its commands in one of those two cannot use the lines as they stand; it must drop the `& ` (bash) or write the program's path in double quotes without it (`cmd`). The prompt does not know which shell the agent's tool uses.
2. **Windows PowerShell (the one that comes with Windows) drops double quotes inside an argument.** `progress 40 'she said "done" twice'` arrives as `she said done twice`. PowerShell 7, `cmd` and bash hand the quotes on. Text with double quotes goes through `--file`, which works in every shell.
3. **The spelling the tool writes for `cmd` breaks an argument that holds a double quote.** It doubles the quote (`"she said ""done"" twice"`), which `cmd` passes on and the program's own start-up then reads as the end of the argument: the command is refused as a usage error and changes nothing. Every other sign arrives whole through `cmd`. Until the spelling is mended (a backslash before the quote is what the program reads), such text goes through `--file`.
4. **The prompt's browser line saves no picture on Windows, in any shell.** As written there it is `chrome.exe --headless --user-data-dir="${TMPDIR:-$HOME/.cache}/..." --screenshot="$WHALESHARK_EVIDENCE/page.png" ...`: the browser is on no PATH, and the variables are spelled for a POSIX shell. What the line must be, for each shell (each saves the picture before it ends):

   PowerShell, both:
   ```
   & "$env:ProgramFiles\Google\Chrome\Application\chrome.exe" --headless --user-data-dir="$env:TEMP\whaleshark-browser-$env:WHALESHARK_RUN-$env:WHALESHARK_ATTEMPT" --screenshot="$env:WHALESHARK_EVIDENCE\page.png" --window-size=1280,900 "http://localhost:$env:PORT/" | Out-Null
   ```
   `cmd`:
   ```
   start "" /wait "%ProgramFiles%\Google\Chrome\Application\chrome.exe" --headless --user-data-dir="%TEMP%\whaleshark-browser-%WHALESHARK_RUN%-%WHALESHARK_ATTEMPT%" --screenshot="%WHALESHARK_EVIDENCE%\page.png" --window-size=1280,900 "http://localhost:%PORT%/"
   ```
   git's bash:
   ```
   "$PROGRAMFILES/Google/Chrome/Application/chrome.exe" --headless --user-data-dir="$TEMP/whaleshark-browser-$WHALESHARK_RUN-$WHALESHARK_ATTEMPT" --screenshot="$WHALESHARK_EVIDENCE/page.png" --window-size=1280,900 "http://localhost:$PORT/"
   ```
   The `| Out-Null` and the `start "" /wait` are not decoration: PowerShell and `cmd` do not wait for a program with a window of its own, and without them the line ends before the picture is saved, so a `report done` typed next would be refused. (The tests use the loopback address in numbers where these lines say `localhost`.)
5. **git on Windows refuses a path longer than 260 characters until it is told otherwise.** With no setting, `git add` of the 322-character file fails. `git config --global core.longpaths true` is needed once on a PC whose projects have such files; with it everything above passes. The tool does not check or set this today.
6. **Nothing starts the keeper at log-on on Windows.** The keeper's service entry is written for macOS and Linux; on Windows there is none. The proof shows that the mechanism works (a scheduled task with the trigger "at log on" that runs `whaleshark engine run`), not that the tool sets it up. Until it does, `whaleshark open` starts the keeper when none runs, and after a restart of the PC the first `whaleshark open` brings the tabs back.
7. **The scenario suite does not play the real keeper on Windows.** It plays every scenario on the engine's stand-in there, and skips the real keeper. `TestTheKeeperOnThisSystem` is the keeper's proof on Windows until that skip goes.

Known before the proof and unchanged by it: on Windows no file is made private by the tool (a file has the permissions of the person's profile folder), "who else can write this" answers "cannot tell", and the keeper cannot ask who is on the other end of its socket file; the folder's permissions are the whole defence. On a PC used by one person this is what every program has. On a shared Windows machine it is unproven.

## What waits

- **winget.** `TestInstallFromTheWingetFiles` runs once the tree has the release program (`build/release/`): it makes a release's files with `go run ./build/release -version 0.0.1 -out <folder> -site <this machine>`, has `winget validate` read the three files, installs from them with `winget install --manifest`, runs the installed `whaleshark version`, and uninstalls. Until then it skips and says `NOT PROVEN`. It has never run. One thing in it differs from a submission: the address the program is fetched from is the test's own machine, since nothing is published; the checksum is the real one.
- **A log-on.** A hosted machine has nobody who logs on: the task is run by hand there. That it fires at a real log-on is step 9 of the checklist.

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
- [ ] Watch one worker's own commands as it types them (`progress`, `ask`, `mail`, `report`). Write down the shell its tool used, and whether any of them failed. If its tool runs `cmd` or git's bash, write down exactly what it typed and what came back (finding 1 above).
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
- [ ] Write down whether the agent used a browser tool of its own or the prompt's one command, and if the command, exactly what it typed (finding 4 above).
- [ ] Type the PowerShell line of finding 4 in a pane yourself, with `$env:PORT` set to a port something answers on and `$env:WHALESHARK_EVIDENCE` to a folder: the picture is there when the prompt comes back.

### 9. Stops, restarts and log-on

- [ ] With three workers at work: `whaleshark engine stop`, then `whaleshark open`. The same tabs and panes are back, and each agent's conversation is resumed.
- [ ] End the keeper in Task Manager in the middle of a run, then `whaleshark open`: the same again, and the record of the run lost nothing (`whaleshark status`).
- [ ] Restart the PC in the middle of a run, log on, `whaleshark open`: the same again.
- [ ] Make the log-on task by hand: Task Scheduler, a new task, trigger "At log on" for your own login, action `whaleshark.exe engine run`, and on the Conditions page untick "Start the task only if the computer is on AC power". Log off and on: `whaleshark engine status` shows a keeper you did not start. Write down whether a console window showed, and whether it stayed.
- [ ] Close the laptop's lid for ten minutes in the middle of a run, open it: the window still answers and no worker was lost.

### 10. An upgrade under a running job

- [ ] With a worker at work and the lead agent in `whaleshark wait`: replace `whaleshark.exe` with a newer build. Write down whether Windows let you replace the file while it ran, and what you had to stop first.
- [ ] The lead agent's `wait` ends with the event `upgraded`, and its next `wait` goes on as before.
- [ ] The panes start themselves again with the new program, or say that they must be started again.

### 11. Other programs at the files

- [ ] Open a run's `state.json` (under `.whaleshark\runs\` in the project) in Notepad and leave it open: commands go on working.
- [ ] Open it in a program that locks what it opens (Excel will do) and run `whaleshark progress`-like work from a worker or `whaleshark answer` yourself: the command says `state_busy` after two seconds and names the file; close the program and the same command works. Nothing in `whaleshark status` is wrong afterwards.
- [ ] Leave the antivirus and the search indexer on for the whole hour, and write down every `state_busy` you saw that you did not cause.

### 12. Long paths and odd names

- [ ] In a project with a file deeper than 260 characters: without `core.longpaths`, start a task and write down what the tool says; then `git config --global core.longpaths true` and the task starts.
- [ ] A project in a folder with a space and an accented letter in its name: a task runs from start to landing.
- [ ] A project on a second drive (`D:`): the same.

### 13. A second login (only if the PC has one)

- [ ] As another login of the same PC: `whaleshark engine status` shows nothing of yours, `whaleshark open` shows none of your tabs, and your project's `.whaleshark` folder and `%LOCALAPPDATA%\whaleshark` cannot be read. Write down exactly what could be read.

### 14. Taking it away

- [ ] `whaleshark init --remove --all` in the project, `whaleshark engine stop`, delete the program file and the log-on task: no process of ours is left in Task Manager, and `%APPDATA%\whaleshark` and `%LOCALAPPDATA%\whaleshark` are the only folders left; delete them.

**When you are done:** the list with its notes goes to whoever integrates the phase. Only when every box is ticked, or its finding is fixed or written down as a known limit, may the documentation say that WhaleShark works on Windows.
