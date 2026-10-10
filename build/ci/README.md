# The build gate

One small Go program, run from the top of the repository. It needs Go and git and nothing else.

```
go run ./build/ci                 every step
go run ./build/ci lines imports   only the steps named
```

It runs every step even after one has failed, prints what each found, and ends with `ci: all passed` or with the names of the steps that failed.

| Step | What it does |
|---|---|
| `build`, `vet`, `fmt`, `test` | `go build`, `go vet`, `gofmt -l` and `go test` over the whole tree, with cgo off |
| `cross` | builds the program as it ships for the five systems and holds each file to the size limit |
| `lines` | counts the lines of every folder against its budget; more than a tenth over fails, and so does a folder under `internal/` with no budget |
| `modules` | the whole module list, not only the direct ones: at most four, each one named |
| `page` | the weight of everything of the page that is not Go |
| `commands` | the number of top-level commands |
| `imports` | the panes, the page and the connection reach neither the store nor the sweep, however many packages lie between, and nothing they reach names the kit's store at all: they read through its reader; the operating system is asked for in one folder only, and the keeper's socket in four |
| `timing` | the commands that must be fast, the middle one of 21 runs each (not on Windows) |
| `security` | the outside checkers below |
| `publish` | nothing private in what git would publish, and no file that names the program the tool once ran on |

## The limits

Every limit is one line of `limits.txt`. A budget moves there, in the same commit as the reason for it.

A line is counted when it is in a shipped file: one with an ending named on the `count` line, outside the files and folders named on the `skip` line (tests, test data and the test kit). Every line counts, blank lines and comments too: a budget that left comments out would be a budget nobody could check with `wc -l`, and would reward code without them less than it should.

## The outside checkers

None of them is part of the program, and the gate installs nothing. A checker that is not installed is skipped and the gate says so; where `CI` is set, a skipped checker fails instead.

| Tool | For |
|---|---|
| `go vet` | the language's own vetting (the `vet` step) |
| [staticcheck](https://staticcheck.dev) | a stricter static analyser |
| [gosec](https://github.com/securego/gosec) | shell strings, weak file permissions, unchecked paths; run twice, see below |
| [govulncheck](https://go.dev/doc/security/vuln/) | our modules against the public list of known flaws |
| [gitleaks](https://github.com/gitleaks/gitleaks) | secrets in the files and in the history |
| [shellcheck](https://www.shellcheck.net) | every shell script in the repository |

A finding that is wrong for this code is silenced at its own line, with the reason in the comment (`#nosec` and the rule's number). No checker is switched off and no folder of the program is left out.

gosec has two rules set aside for every folder, because the program is a command a person runs as themselves on files they name: G304 (a file opened by a path held in a variable, which is every file this program opens) and G104 (an error nobody reads: what is left is a close after the work is done or has failed, a line to a terminal or a connection that has gone, and the note of who holds a lock). `security.go` says the same beside the rule numbers.

**The stand-ins under `test/` are held to a lighter rule.** The engine's double and the fake agent are in no program we ship. A test tells them through their environment which program to start, which socket to call and which files to touch, so for those folders gosec also leaves out G204, G702, G703 and G704. It runs once over everything else and once over them.

## The publish check

`publish` reads every file git would commit, as it is in the folder and as it is staged, and fails on a private file name, a path under a home folder, a private key, the shape of a well-known token or access key, or an email address that is not GitHub's no-reply one or an example. It also fails when a published file, by its name or on any line, names the terminal program WhaleShark ran on while its own engine was being built: the tool depends on no other program, and the tree says so by not holding the name. One line of `README.md` may hold it, for the credits. `message FILE` checks a commit message, and `history [RANGE]` checks who made each commit, what it says and what it adds.

To run it before every commit and every push:

```
git config core.hooksPath build/ci/hooks
```
