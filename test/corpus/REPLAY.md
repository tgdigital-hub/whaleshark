# Replaying the corpus through the screen reader

The screen reader (`internal/screen`) turns what a program prints into a picture of cells. Its tests feed it every recording of this folder and compare what comes out with pictures a person has read and approved.

```
pictures/     NAME.txt for recordings/NAME.rec: the approved pictures
replay/       the tool that shows and approves them (a test tool)
corpus.go     reads a recording, writes a picture as text
synth.go      writes the synthetic streams
```

## The tool

From the top of the repository:

```
go run ./test/corpus/replay show test/corpus/recordings/vim-80x24.rec   the last picture of a recording
go run ./test/corpus/replay approve                                     write every picture again
go run ./test/corpus/replay synth                                       write the synthetic streams again
```

`approve` writes the pictures as the reader draws them now. It is run after a change that was meant to change a picture, and what it wrote is read before it is committed: `git diff test/corpus/pictures` shows exactly which cells moved.

When the variable `WHALESHARK_CORPUS_EXTRA` names a second folder with the same layout, the tests replay its recordings as well and `approve` writes its pictures there. Without it, this folder is all there is.

## A picture file

Four pictures of one recording: after each quarter of the pieces it arrived in. A recording ends when its program ends, so the last picture of a full-screen program is the screen it left behind; the three before it are the program at work. A picture can fall in the middle of a redraw, and then it shows half of one.

```
== less-80x24 after 26 of 51 ==
cursor 0,23 shown
|gamma line 24: the quick brown fox jumps over the lazy dog                      |
...
3:0-9 fg=1 bold
3:12-20 bg=#3c005a underline
```

First the cursor's column and row, counted from 0, and whether it shows. Then each row between bars; the right half of a wide character takes no room, so a row with wide characters is shorter on the page. Then every run of cells that is not plain, as `row:from-to` and what it has: `fg` and `bg` are one of the 256 colours by number or a full colour as `#rrggbb`, then `bold`, `dim`, `italic`, `underline`, `reverse`, `strike`.

## The synthetic streams

`recordings/synth-*.rec` are not recordings. A recording of a coding agent holds its whole interface text, which is its maker's and not ours to publish, so no such recording is in this folder. In their place `synth.go` writes streams around words of our own that use the kinds of sequence such an agent was counted sending, in about the proportions counted:

| Stream | What it is |
|---|---|
| `synth-classic-*` | drawing on the first screen: each frame inside the "draw in one go" mark, down to the bottom of what was drawn, carriage return, up n lines, only the cells that changed written, words placed with "go to column" and no space printed, erase to the end of the line, lines ended `CR CR LF`, the cursor hidden and parked in the typing line, the title set at each change of state, the page scrolling by line feeds at the bottom |
| `synth-classic-plain-80x24` | the same with no mark at all, as in a terminal that answers no question |
| `synth-fullscreen-*` | drawing on the second screen: home, then down and forward to each row that changed, almost no line feed, the mouse asked for, the cursor placed by row and column at the end of each frame |

Each begins with the questions an agent asks a terminal, among them the three sequences that end in `m` and are no style, and each has wide and joined characters, the eight colours, full colour, and a table drawn with lines.

The generator keeps its own model of the page, and a test holds the reader to it: after every frame the text the reader shows must be the text the generator meant to draw. Another test fails if the files here are not what the generator writes.

A synthetic stream proves that the reader handles those sequences in those combinations. It does not prove that a real agent sends nothing else; only a replay of real recordings shows that.
