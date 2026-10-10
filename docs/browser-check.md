# A browser check

**A browser check has been played by a scripted agent only.** No real agent has yet been given one. The one command below has been run by hand on one Mac and nowhere else.

WhaleShark starts no browser and drives none. Agents have browser tools of their own. What the tool adds is the bookkeeping: a place for the proof, a rule that the proof must exist, and a way for you to see it.

## Marking a task

```
whaleshark task add T5 "sign-up page" --brief briefs/T5.md --check 'make test' --browser-check
```

The brief says what to look at.

## What the worker gets

Its tab carries three values, and its prompt a short section that says how to use them:

| Value | What it is |
|---|---|
| `$PORT` | the port to run the site on, bound to the loopback address; `$PORT_BASE` is the first of the ports that are the task's own |
| `$WHALESHARK_EVIDENCE` | where the screenshots go: the folder `evidence` of that attempt, inside the project's private `.whaleshark` folder |

The worker runs the site, looks at it with its own browser tool, without a screen and with a browser profile of the task's own, never yours, and saves screenshots into the folder. A trace file or a short note may go there too. It is told never to switch the browser's sandbox off. It is told, not held: a worker with its own browser tool does as that tool does.

## The one command without a browser tool

A worker that has no browser tool is given one command in its prompt. On macOS it reads:

```
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless --user-data-dir="${TMPDIR:-$HOME/.cache}/whaleshark-browser-$WHALESHARK_RUN-$WHALESHARK_ATTEMPT" --screenshot="$WHALESHARK_EVIDENCE/page.png" --window-size=1280,900 "http://localhost:$PORT/"
```

The prompt writes the loopback address as its four numbers where this page says `localhost`. On Linux the program named is `chromium`; on a server it is the Chromium that `server setup` installed, found through `$PLAYWRIGHT_BROWSERS_PATH`; on Windows it is `chrome.exe`. The Linux, server and Windows forms are written from memory and have never been run.

## The rule

- `report done` on such a task is refused until a screenshot lies in the folder that was saved since the worker began or was last sent back.
- A screenshot is a file that begins as a PNG, a JPEG or a WebP does. A note or a trace alone is no proof.
- An attempt keeps at most 40 files and 50 MB of evidence.
- `accept` reads the folder again and refuses a browser-check task without a screenshot.

## Seeing it

`whaleshark show T5` has a line `Evidence` with the count and every file's name, and a line `Site`: "answering on port N" with its address while the worker's site still answers, "site stopped" once it is gone. The lead agent is told to read each screenshot against the brief before it accepts.

On the page, the task's screen shows the screenshots, newest attempt first, and anything that is not one of the three picture kinds is offered as a download. While you are connected with `whaleshark connect`, the running site is forwarded to your own computer under the same port number.

A site that answers on the IPv6 loopback address only is not seen; `show` says so and says how to bind it.

## Treat the evidence as private

A screenshot can show a secret (the worker's prompt does not warn it of that; your brief can), and a trace file from a browser tool can hold every request the page made, cookies included: **treat a trace file like a password.** Evidence lies in a folder only your login can read and is served only to a signed-in session.

## What this does not give you

Nobody but the worker sees the page live. A screenshot can miss what a click would show. And a worker that opens a web page reads whatever that page says ([protection.md](protection.md)).
