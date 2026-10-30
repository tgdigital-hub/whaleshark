# The rehearsal

A plain user on a Linux server, with no root, from the installer to a merged job. It is an ordinary Go test, so `go test ./build/rehearse` plays it on any machine with `sh`, and the gate's `test` step plays it on every hosted machine but Windows. The gate's job `rehearse` plays it in full on Ubuntu, as a login made for it.

| Step | How it is played | Where |
|---|---|---|
| Install | the program built as a release is, signed with a key of the test's own, put in place by the real `install.sh --from` | everywhere; the login's real home in the job, a temporary one elsewhere |
| Doctor | the installed program, as that login | everywhere; its rows of the service only on Linux |
| The user service | `init --at-login`, the real `systemctl --user`, the doctor's two rows, a killed keeper that comes back, `init --remove --all` | where `REHEARSE_SERVICE` is set; elsewhere the unit's text is read and the test says so |
| A job | the installed program on the real keeper, a stand-in for `gh` and fake agents: a stale clone, two code tasks and a readonly one, the merge lock, a lock taken twice, `close_on_accept` | everywhere |

## What it does not cover

- A real `gh`, a real forge and real checks: the stand-in merges for real onto a bare remote, and its checks are one file.
- A real agent: the fake agent of `test/fakeagent` stands under the name `claude`.
- A real logout and a reboot. In the job the login's services run through lingering with nobody logged in as it, and a killed keeper comes back; that the panes and agents come back after a keeper's restart is the scenarios' part.
- The phone road, and a worker's deny list in a real agent.
