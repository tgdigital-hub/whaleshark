# The page and the phone

**Nothing on this page has met a real phone, a Tailscale network, a Cloudflare account or a phone maker's push service.** The page has been opened in one browser on one Mac. Both roads for a phone are written from their makers' manuals and tried by tests.

## The page

`whaleshark dash` is a small server of your own login. It listens on a socket file in your own folders and on nothing another machine can reach. `whaleshark connect SERVER`, on your own computer, carries your browser to it through SSH and signs you in with a link that works once, for sixty seconds. There are no passwords and no user names: whoever can log in as you can open your page. A session lasts thirty days and is renewed by use; `whaleshark dash stop --forget` ends every session, phones included.

The page shows what the panes show: what waits for you, the team, the plan, the messages, each task with its screenshots. Its buttons answer an item, accept or send back a result, start or stop a task, send a note, and press Stop all, Resume, Catch me up, Do not disturb and Mute. The page can never land work on your branch, approve a project's settings (`trust`), delete or cancel anything, or answer an agent's own prompt; those stay typed commands.

## A phone: two roads, one per server

There is no app. The phone's browser opens the same page, laid out in one column. It needs a server; a single computer has no page for a phone. The server's administrator chooses the road.

| | Tailscale | Cloudflare |
|---|---|---|
| you need | a Tailscale network with HTTPS certificates switched on; Tailscale on the server and on each phone | a domain on a Cloudflare account of your own, with Cloudflare's login service (Access) |
| where the page can be found | nowhere outside your private network | at a public name; anyone can reach Cloudflare's login screen for it |
| who can read what passes | nobody between phone and server | **Cloudflare**, screenshots included |
| how the page knows who calls | the identity Tailscale reports, which the page has to believe | a pass Cloudflare signs, which the page checks itself on every request |
| people a machine | three | eleven |
| a program on the server that is not ours | Tailscale, which you install | Cloudflare's connector, which `server tunnel` installs |

**Tailscale.** Install Tailscale on the server yourself and sign it in; WhaleShark never installs or calls it for anything else. Then, as the administrator:

```
sudo whaleshark server phone --user ana
```

**Cloudflare.** As the administrator, once for the machine, then for each person an Access application of their own whose rule admits that person's email addresses and nobody else's:

```
sudo whaleshark server tunnel --team TEAM
sudo whaleshark server phone --user ana --name ws1.example.com --aud TAG --email ADDRESS
```

`TEAM` is the first part of your `TEAM.cloudflareaccess.com`, and `TAG` is the tag Cloudflare gives that person's application. `server tunnel` says first that it installs a program that keeps a connection open to somebody else's network, and shows one link of Cloudflare's to open. The routing lives in a file on the server that belongs to root, so the Cloudflare account cannot send a name to another port. A request without Cloudflare's pass, or with one for another person or another application, gets an empty refusal; with Cloudflare's keys out of reach everything is refused. *In the code as it stands the connector's version and key are not pinned yet, and `server tunnel` refuses to install it; a release fills both in.*

Choosing Cloudflare means trusting Cloudflare: on this road it could do what a paired phone can. Put a second step on your Cloudflare login and on the mailbox it uses; whoever takes either over can show a false page under your name to catch an approval.

`--off` on either command takes the door away. Switching roads means pairing every phone again.

## Pairing a phone

1. In your own login on the server: `whaleshark dash phone on`. It prints the address to open.
2. Open the address on the phone. It shows a number and nothing else.
3. Approve that number where you are already signed in. There are three ways:
   - on the page in your laptop's browser, where the request appears as an item;
   - in the action pane of the window, as the same item;
   - at a terminal: `whaleshark dash phone list` shows the phone that asks, and `whaleshark dash phone approve NUMBER` lets it in (`refuse NUMBER` does not).

The phone then shows the page by itself. One request is open at a time and it is gone after two minutes. `whaleshark dash phone list` shows the paired phones, `whaleshark dash phone forget PHONE` ends one at once, and `whaleshark dash phone off` shuts the door.

A phone can do what the page can and no more, and it has no tabs to go to.

## Nudges

When something waits for you, the tool nudges you: a sound and a pop-up on the computer, and a notice on a paired phone.

**A nudge reaches a paired phone only while `nudge.phone` is on**, and only after you pressed "Nudge me on this phone" on that phone, which hands over the address its browser gave it. It is on unless you switch it off:

```
whaleshark set nudge.phone off
whaleshark set dnd on            # do not disturb, until you set it off
whaleshark set mute on           # no sound
whaleshark doctor --notify       # sends one test nudge
```

The notice for a paired phone is sealed for that one phone and handed to the phone maker's push service, which carries it and cannot read it. Its text shows on a locked phone.

Without a server, or beside it, `url` under `[notify]` in your own `config.toml` names an address of a notice service of your choosing, and each nudge is one request to it. That service is somebody else's and reads what it carries, so it gets the title, which names the task, and the text only if you also set `full_text = true` there.

## What cannot be held here, by design

- On Cloudflare's road, whoever acts as a paired phone can send the lead agent a note, and a note is an instruction to an agent that runs as you. "The page cannot land or trust" is true of the page only.
- On Tailscale's road every person's address is one name with another port, and a browser sends a name's cookies to every port. A co-worker's page receives your phone's cookie if your phone opens it; only the identity Tailscale reports and proven port rules keep it useless to them.
- Whoever can reach a door can keep its one pairing request busy. Five refusals slow the next.
- More than twenty refused requests in a minute shut a Cloudflare door for a minute, for you as well.
- A server you reach with `connect --terminals` can write your computer's clipboard at any moment.
- An unlocked stolen phone is you, for everything the page can do, until you `forget` it.
- An agent of your own login can read the page's key, and so sign in or approve a phone as you ([protection.md](protection.md)).
