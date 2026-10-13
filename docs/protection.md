# What an agent can and cannot do, and what is and is not protected

Five security reviews have tried this program, each on one Mac; the fourth also tried the release: its files, its installer, its packages and the hosted jobs that make it, and the fifth what came after: Fleet Mode and the fence between agents' tabs, the note typed into the orchestrator's chat, agent tools wired in by a description, saved fleets, settings through the orchestrator, the saved profile, the guardrails, the interface for apps and the version notice. **None of it has been tried with a second login, on Linux or Windows, on a real server, from the public internet, or with a real phone.** What follows is what the program does, and what it cannot do for you.

## The two limits

They are true of this tool and of every tool of its kind.

1. **An agent runs as the person it works for, and inside one login there is no wall.** Everything your login can reach, an agent can reach, the tool's own controls included.
2. **An agent believes what it reads.** A web page, a file in a repository or a message can hold text written to steer it. No tool filters that reliably.

So the rules below are fences against **mistakes**. Read "the tool never does X by itself" as exactly that, never as "X cannot happen".

## What the rules do for an honest agent

Every command works out who is calling: a worker, the lead agent, a person, or a tab that belongs to no run.

| Caller | May |
|---|---|
| a worker | report, note its progress, ask a question, read its mail. Nothing else: it cannot accept its own work, answer its own question or start agents |
| the lead agent | plan tasks, start and stop workers, accept or send back results, answer workers, put items in front of you |
| a tab bound to no run | look, and change nothing in a run |
| you | everything; and some things are yours alone: Stop all and Resume, `trust`, undoing an answer, a note to the lead agent, and, unless the project's approved settings say otherwise, `land` |

A second lead agent started by mistake can look and cannot change the run. `--human` marks a command as yours; the tool never prints it to an agent, refuses it from a pane it knows as an agent's, and for Claude Code the agent's own harness turns down a command line that names it. A worker's work reaches the collected branch only after its check has passed on the merged result.

## What an agent that goes wrong, or is steered, can still do

| It can | How | What the tool does about it |
|---|---|---|
| call itself you | add `--human` in a pane of its own making or through a wrapper; clear the variables that mark it as a worker; run the command under a pretended terminal | records every action taken as you with where the command **says** it came from, hands it to the lead agent, shows it in the task's history, and counts those that came from neither a pane nor the page under "done as you" on the strip and in every catch-up |
| pass as a button on the page | clear its variables and set the page's mark | the record says "page". **This one is not counted under "done as you"** |
| have a sentence typed into the lead agent's chat as yours | call itself you, or pass as the page, and send a note with `whaleshark tell lead` | the line begins with where the command says it came from ("typed at a terminal:", "from the page:", "from your phone:"), and the note is an event in the record; a worker, a fleet leader and the lead agent itself are refused, and no other tab is ever typed into |
| answer an item, approve a sign-off, pair a phone, press Stop all | the same; or type into the action pane | the same record. An answer typed into the pane looks exactly like yours; it can be undone until something has used it |
| type into any other tab, the lead agent's included, and so answer another agent's permission prompt | every agent of a login can reach that login's keeper | a call that types, focuses or closes is refused when it comes from a pane the keeper knows as an agent's; an agent that leaves the pane's name out passes |
| approve a project's settings | `whaleshark trust`, and "yes" | shows what changed since the last approval; the approval stays on every screen, in red, until the next |
| accept its own work | as you, or with another attempt's token file | the check still runs on the merged work; the record says who accepted |
| merge into your main branch on that machine | `git merge`, with or without the tool | nothing |
| change the record, another worker's copy of the code, or the approvals file | edit the files; they are its login's | nothing, and an edited record leaves no trace |
| make git run its program inside the lead agent's commands | write the repository's own git settings or hooks, which git obeys | nothing |
| read the page's key, and sign in or approve a phone as you | the key is a file of its login | nothing |

**One steered worker can type into every other tab of its login. This is not solved.**

## What the tool never does by itself, under any setting

1. Answer or dismiss an agent's permission prompt or its question whether it trusts a folder, or send any key to an agent. Stop all types nothing and ends no program: each agent stops at its next step.
2. Start an agent with wider permissions than the project's approved settings say. It has no default that widens anything.
3. Run a project's setup script, landing check, team file or changed agent arguments that you have not approved by content with `whaleshark trust`. A change needs approval again. The approval is kept in your login's own folder, never in the project.
4. Use a `.whaleshark` folder your login did not make. One that came with a repository, an archive or a copy is refused by every command until you type `whaleshark init --adopt`. Adopting takes all of it on: its runs, their checks, and the sentence of each saved fleet, which your agents then read.
5. Put work into your main branch, or push anything, outside `land`.
6. Delete work that is not proven merged.
7. Install software or change another login's files, outside `whaleshark server`, which only the administrator can run.
8. Restart, stop or retry an agent on a guess.
9. Send anything about you anywhere. There is no telemetry and no crash upload, and nothing is ever downloaded or installed for you. One call out is made unasked, and can be switched off: with "Tell me when a new version is out" on, as it comes (Settings › Nudges, or `whaleshark set update.notice off`), the window asks GitHub once a day for the number of the latest release. It is one request to this project's own releases address, carrying the program's name and nothing else, no version, no identifier; the answer is read for a version number and followed nowhere. GitHub sees the address the request comes from, as any site does. On a server no login's window asks: only the administrator's own `sudo whaleshark server status` does. The only other calls out are ones you set up (a notice address, a paired phone's nudge, on the Cloudflare road its public keys and one request of `doctor` to your own public name) or typed (`land --pr`, `connect`).

## The real wall: what you give the login

| Thing | Give the login the agents run in | Not |
|---|---|---|
| your git host | a token limited to the repositories being worked on, able to push branches and open pull requests | a token for the whole account; the right to push to main or to change its protection |
| the main branch | protected at your git host: changes arrive by pull request, approved by a person | trust in the tool's `land` rule, which an agent can step over |
| SSH | no keys that open other machines; agent forwarding off | your laptop's own key copied to the server |
| `sudo` | none; on a server the administrator login is separate and runs no agents | |
| other secrets (cloud, production) | none; test values only | real ones in `.env` files |

On your own computer the same holds with less to hold it: the agents run as you, with everything your login can reach.

## Who could attack, and what is left

| Who | The defence | What is left |
|---|---|---|
| another person on the same server | a login each, private homes, socket files, a port block of your own, a cap on agents and memory | the port rules are **unproven**; until `doctor --server` has proven them, a neighbour can reach your agent's site or stand in for it. The administrator and the kernel cross every wall |
| a hostile web page or link an agent opens | a browser profile of the task's own; narrow tokens; a protected main branch | a steered agent can do everything in the table above |
| a hostile repository | settings that can run or reach something count only after `trust`; a record the login did not make is not used; paths are held inside the project; names and titles are shown with control characters removed | `trust` does not keep an agent the repository has steered from approving it; the approval is at least shown in red |
| a compromised dependency | four modules, pinned with checksums; no code loaded at run time | the agent programs and, on a server, Node and Chromium are other people's software |
| someone on the network | on a server only SSH answers, keys only; downloads are checked against a signed list, and the list says which version it is | a flaw in SSH or Cloudflare themselves. `install.sh` is the one file nothing checks before it runs: whoever can change the copy you fetch can change what it does, so check it by hand first where that matters (below). The apt index carries no end date: whoever serves the apt folder can go on serving an older one, rightly signed |
| whoever takes over the maintainers' account at the git host, or gets hold of the release key | the key is a secret that one hosted job is handed, for a tag only and after a maintainer's approval each time; that job can write nothing, and the job that writes the release holds no key. Anyone can build a release again from its tag and compare the lists of checksums (`build/release/README.md`) | **a release a thief signed is a signed release.** A key cannot be taken back: the only remedy is a new key in a new `install.sh`, and a notice in the README. The program files carry no signature of Apple's or Microsoft's |
| Cloudflare, or whoever takes over your Cloudflare account or its mailbox | pairing, which the account does not reach; routing in a root-owned file on the server | **against Cloudflare itself there is none**: it could do what a paired phone can |
| a stolen phone or laptop | `dash phone forget`, `dash stop --forget`, removing the SSH key | until then the thief is you, for what that device could do |
| a web page open in your own browser | the page has a host name of its own and accepts no other; every change needs the session's token and the page's own origin | |

More of the same kind, for the page and the phone, is in [phone.md](phone.md); for a shared machine, in [server.md](server.md).

## Checking a release by hand

`install.sh` is named in the signed list like every other file of a release, so the one file that nothing checks before it runs can be checked by you: fetch `install.sh`, `SHA256SUMS` and `SHA256SUMS.sig` of one version, put the release key's line from the README into a file, and run `ssh-keygen -Y verify -f THAT-FILE -I release -n file -s SHA256SUMS.sig < SHA256SUMS`, then compare the checksum of `install.sh` with its line in the list. The fingerprint the script prints at the end proves nothing by itself: a changed script could print any.

## What the terminal engine holds

Nothing a program prints is passed on to your terminal: the keeper reads it into cells and the window draws only what the keeper composes. A program in a pane cannot retitle or lock your terminal or make it type text back, and can write your clipboard only if you allowed it (`programs` under `[clipboard]` in the engine's `settings.toml`: you are asked each time unless you set it on or off, and no agent's `set` and no request from a browser changes it). The keeper opens no network port; its socket is a file only your login can open. On Windows the folder's permissions are the whole of that defence, and it is unproven on a shared Windows machine.

## Files

Everything of ours is created private to your login: the `.whaleshark` folder in a project, the settings, state and cache folders, the socket, the key, the tokens, the evidence and the tasks' copies of the code. Nothing of ours is created in a shared temp folder. `init` refuses a syncing folder and a project folder another login can write to. Briefs, results, logs and evidence are never put in git and never written to a system log. Free text typed on a command line can be seen in the process list of a shared machine: use `--file` there.
