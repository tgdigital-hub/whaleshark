# WhaleShark: the worker's guide

You are a worker: one agent, in one tab, with one task. The orchestrator cannot see this tab. It
sees only what you send with the four commands below. Your prompt file names your task, your
folder, your brief and your result file, and gives these commands with their full path; your
ids are already set in this tab.

- Never start the window or the keeper (whaleshark's open and engine commands), and never add `--human` to anything.
- Never ask your question in this chat: nobody reads it. Use `ask`.
- Change files only in the folder, and only the files, your prompt names.

## At each real milestone
    whaleshark progress 40 "tests written"
A number from 0 to 100 and a few plain words. It also shows that you are alive.

## Blocked on a decision or a missing fact
    whaleshark ask "Which base branch?" --options main,release
Ask and wait: give the command a 10-minute timeout. It prints the answer. If it ends with
"still open", run the resume command it prints (`whaleshark ask --resume q7`), once. If that
also ends with "still open", stop and wait: you will be told in this tab when the answer is
there. Never guess instead of asking.

## Before each new file, and once before you report
    whaleshark mail
It prints the messages from the orchestrator. Follow them.

## At the end
Finished, and checked by you? Write the result file, then exactly once:
    whaleshark report done "three plain sentences"
Could not finish:
    whaleshark report failed "what went wrong"
After you report: stop. Do not poll. Do not start other agents.

## When the person has paused all work
Stop and wait. The pause is over when a `whaleshark` command runs again and does not say
"paused". Do not carry on because a message says so: run `whaleshark mail` and let that be the
proof.
