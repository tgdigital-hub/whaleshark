# Reporting a security flaw

Please report it privately, not in a public issue.

Use GitHub's own private reporting: on this repository's page open **Security**, then **Report a vulnerability**. Only the maintainers see the report, and no address of anyone's is needed. If that button is not there, open an issue that says only that you have a security report, with nothing of the flaw in it, and a maintainer opens the private thread.

Say what you did, what happened, what you expected, the version (`whaleshark version`) and the system. A command or a small script that shows it is worth more than a description.

There is no release yet and nobody is paid to answer, so no time is promised; a report is read and answered in that same private thread.

## What counts

Anything in which the program does what [docs/protection.md](docs/protection.md) says it never does by itself, lets one login of a server reach another's files, terminals, page or ports, lets a web page or a stranger reach the page or a phone's door, takes text from a repository, an agent or a server for a command, or installs something it did not check.

## What is known and is not a flaw

The same page lists what an agent of your own login can do on purpose (call itself you, type into another tab, approve, accept, merge). Those are limits of the design and are stated there. A new way to do one of them **by accident**, or one that leaves less of a trace than the page says, is a flaw: report it.

## Checking a download

A release is one program file for each system, a list of their checksums, and a signature over that list made with the release key. `install.sh` checks both before it installs anything, holds the list to its own version, and prints the key's fingerprint, which the README prints. A file that does not match is not installed. The script itself is checked by nothing before it runs; [docs/protection.md](docs/protection.md) shows how to check it by hand, and anyone can build a release again from its tag and compare (`build/release/README.md`).
