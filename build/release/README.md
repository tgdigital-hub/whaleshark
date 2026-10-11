# Making a release

One small Go program, run from the top of the repository. It needs Go and git and nothing else.

```
go run ./build/release -version 1.2.3
```

It writes into `dist/` (or the folder `-out` names):

| File | What it is |
|---|---|
| `whaleshark-<system>-<processor>` | the program for five systems, built without debug symbols, with cgo off and with no path or name of the machine in it |
| `install.sh` | the installer, with this version and the release key filled in |
| `LICENSE`, `NOTICE` | the licence, and the licences of the four modules compiled into the program, which ask to go with it |
| `SHA256SUMS` | a first line `# whaleshark 1.2.3`, then the checksum of each of the eight files above. `install.sh` refuses a list whose first line names another version than its own |
| `apt/` | a `.deb` for each of the two Linux files, the index apt reads (`dists/stable/Release`), and `whaleshark.asc`, the public half of the key that index is signed with |
| `brew/whaleshark.rb` | the Homebrew formula: each file's address and checksum |
| `winget/` | the three files of a version in Windows' package list |

It signs nothing, publishes nothing and changes nothing in the tree. The filled-in scripts are laid over the tree's own for the build, so the program carries the same `server.sh` with the release's values and without the folder its tests run in.

## The same bytes every time

A second run on the same commit gives the same files, on any of the three systems. So anyone can check a release: fetch the tag, run the line above with the release's version, and compare `dist/SHA256SUMS` with the one the release carries.

What that rests on: the Go that `go.mod` names and no other (the program refuses another and says how to get the right one); no word of the checkout's state in the files; every packed file carrying the time of the commit (or `SOURCE_DATE_EPOCH`, where that is set).

## The two keys

The **release key** is an SSH key. Its public half is the `signer` line of `settings.txt`, one allowed-signers line; `install.sh` carries that line and the README prints it. Its private half signs the list:

```
ssh-keygen -Y sign -f KEY -n file dist/SHA256SUMS
```

which writes `dist/SHA256SUMS.sig`. Anyone checks it with the line alone, in a file:

```
ssh-keygen -Y verify -f SIGNERS -I release -n file -s SHA256SUMS.sig < SHA256SUMS
```

The **apt key** is an OpenPGP key, because apt reads no other signature. It signs the index and nothing else:

```
gpg --clearsign -o dist/apt/dists/stable/InRelease dist/apt/dists/stable/Release
```

and its public half (`gpg --armor --export`) is the file a machine is given before its first `apt-get install`. The program inside a package is the file the release key signed.

Its public half is in the repository, `build/release/whaleshark.asc` (fingerprint `F997 9264 BFC0 FF2F CD5D 803E 6472 B33B 2405 C475`), and every release carries it as `apt/whaleshark.asc`, at the top of the apt folder: where that folder is served, the key is at `<address>/whaleshark.asc`, which is what the README's three lines fetch. A tag's run refuses a secret that is not this key.

**A release also works without the apt key**, as it did before the key existed and would again if the secret were taken away: the list is signed as ever, the apt index is not signed, and the draft's `packages.tar.gz` holds the `brew` and `winget` folders and no `apt` folder, because an index nobody signed is not handed out. The run says so in three notices. The `.deb` files are still built on every run, and a run without the key installs one with `dpkg` to see that it holds the program.

Neither private half is ever in the repository. `settings.txt` holds what is public: the signer line, and the two pins of Cloudflare's connector that `server.sh` is given.

## On hosted machines

`.github/workflows/release.yml` does the above with no step written by anyone else.

- **A dry run** starts by hand, or by itself when this folder or the workflow changes. Started by hand it can be told to go `without` a key for apt, as a tag does while that key does not exist. It makes two keys inside the run (one, when told `without`), builds, signs, and on macOS, Linux and Windows builds again, holds the second build against the signed list, installs from those files (`install.sh`; `apt-get` on Linux; `brew` on macOS; by hand on Windows) and runs the program. Then it changes one byte of the program and sees it refused. It publishes nothing, and its keys end with it.
- **A tag `v1.2.3`** does the same with the real keys, and then makes a draft release holding the five files, `install.sh`, `LICENSE`, `NOTICE`, the list and its signature, and `packages.tar.gz` (the `apt`, `brew` and `winget` folders with the signed index). A person publishes the draft.

The real keys are two secrets, `RELEASE_KEY` and `APT_KEY`, of an environment named `release`, which lets only tags `v*` through and asks a person each time. The job that reads them can write nothing to the repository; the job that writes the draft holds no key; no file passes between them, only the two signatures, and each job builds the files again and holds them against what was signed.

With a key that never leaves a person's desk, the same lines above are typed there, and the two signatures are added to the draft by hand.
