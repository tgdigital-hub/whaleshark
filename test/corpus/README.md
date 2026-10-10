# The corpus: what real programs print

Recordings of real programs running in a pseudo-terminal, saved byte for byte with the time each byte arrived. They are test data for a screen reader: feed one in, and the picture at the end must be the picture the program drew.

```
recorder/     the tool that makes and replays a recording (a test tool; Go, no extra module)
scripts/      what was typed in each recording, step by step; each begins with a note in plain words
recordings/   <program>-<columns>x<rows>.rec
```

## Replaying one

Make your terminal window exactly the size in the file's name (or larger; smaller shows it wrongly), then:

```
go run ./test/corpus/recorder play test/corpus/recordings/vim-80x24.rec            as it happened
go run ./test/corpus/recorder play test/corpus/recordings/vim-80x24.rec -speed 4   four times faster
go run ./test/corpus/recorder bytes test/corpus/recordings/vim-80x24.rec > out.bin the program's bytes alone
```

`play` writes the program's bytes to your terminal with the recorded pauses; nothing is typed and no program is started. A full-screen recording leaves your terminal as the program left it, which is back to normal when the recording runs to its end. If you stop it half way, type `reset`.

## The format

Text, one event a line, so that a recording can be read, searched and compared:

```
whaleshark recording 1
size 80x24
argv ["/usr/bin/less" "gamma.txt"]
env ["PATH=/usr/bin:/bin" "TERM=xterm-256color" "HOME"]
0.012607 o "\x1b[?1049h\x1b[22;0;0t..."
0.546767 i " "
3.100000 s 100x30
9.886521 x 0
```

After the four header lines: seconds since the start, then `o` (printed by the program), `i` (typed into it, or an answer given to it), `s` (the size changed) or `x` (it ended, with its exit code). Bytes are written as a Go string in ASCII (`\x1b`, `\r`, `─`), which `strconv.Unquote` reads back exactly. A variable in `env` without a value was handed on from the recording machine and its value left out.

## Making one

```
go run ./test/corpus/recorder record -size 80x24 -script test/corpus/scripts/less.script \
    -out new.rec -dir <a folder of made-up files> -env PATH=/usr/bin:/bin -env TERM=xterm-256color -- /usr/bin/less gamma.txt
```

The script language (`wait`, `quiet`, `expect`, `send`, `reply`, `size`, `end`) is described at the top of `recorder/main.go`. The program gets the variables named with `-env` and no others.

**The recorder is not a terminal.** It obeys nothing the program prints. A program that asks the terminal a question gets an answer only where the script has a `reply` line, and then exactly those bytes. So each recording says in its own `i` lines what the program was told, and a screen reader that would answer differently should expect the program to behave differently.

## What is here

Every program was recorded at 80x24, 120x40 and 50x15, on macOS, in a folder of made-up files (`alpha.txt`, `beta.txt`, a 200-line `gamma.txt`, one very long line, a file of wide and joined characters, a small Go file).

| Recording | Program | What was done |
|---|---|---|
| `zsh-*` | `zsh -f`, prompt `$ ` | commands with output; 60 lines that scroll; the 8, 256 and full colours; a line that wraps; wide characters; completion with Tab; history with the up arrow; editing in the middle of a line; a command interrupted with Ctrl+C; the bell; `clear` |
| `bash-*` | `bash --norc --noprofile` (the system's 3.2), prompt `$ ` | the same script |
| `vim-*` | `vim -u DEFAULTS -i NONE` | its questions answered; moving, a search, typing in insert mode, deleting a line, line numbers, paging, a horizontal and a vertical split, syntax colours, the wheel, a redraw, quit |
| `less-*` | `less` | paging both ways, a search and its next match, end and start, line numbers, a jump, quit |
| `pager-*` | `less` again, on a made-up page with bold headings and underlined words | paging, a search that lights its matches, long lines cut instead of folded and the view moved sideways, only the matching lines shown, a mark and the jump back, the status line, quit |
| `top-*` | `top -pid 1 -stats pid,command,cpu,time,mem -s 1` | six redraws of one process, quit |
| `nano-*` | the system's `nano` (which is pico on a Mac) | typing, moving, a search, cutting and pasting a line, leaving without saving |

**Recordings of proprietary programs are kept out of this repository.** A recording holds the full text a program shows, and a program's own interface text, or a manual page, is its maker's and not ours to publish; so recordings of coding agents and of `man` are made and kept privately, in the same layout. The replay tests take a second folder of recordings from the environment variable `WHALESHARK_CORPUS_EXTRA` when it is set, and run on this folder alone when it is not.

## Adding a recording

A recording holds everything the program printed, and this folder is public. Record in a throwaway folder of made-up files, with a plain prompt and an empty home folder, and read the result before committing it: no name of a person, a login or a machine, no home-folder path, no address, no session id, no token, and no text that belongs to someone else: the files shown are made up, in our own words. If a program cannot be kept from printing one of these, do not commit that recording.

## Replaying

`REPLAY.md` in this folder has the tool that replays a recording, the form of an approved picture, and the streams the tests write themselves.
