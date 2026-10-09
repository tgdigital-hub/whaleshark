// Command whaleshark is the one program file: the command, the panes, the
// page and the connection from your own computer.
package main

import (
	"os"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/connect"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/dash"
	"github.com/tgdigital-hub/whaleshark/internal/dashweb"
	"github.com/tgdigital-hub/whaleshark/internal/doctor"
	"github.com/tgdigital-hub/whaleshark/internal/evidence"
	"github.com/tgdigital-hub/whaleshark/internal/gitwt"
	"github.com/tgdigital-hub/whaleshark/internal/guide"
	"github.com/tgdigital-hub/whaleshark/internal/herdr"
	"github.com/tgdigital-hub/whaleshark/internal/hook"
	"github.com/tgdigital-hub/whaleshark/internal/inbox"
	"github.com/tgdigital-hub/whaleshark/internal/integrate"
	"github.com/tgdigital-hub/whaleshark/internal/launch"
	"github.com/tgdigital-hub/whaleshark/internal/mail"
	"github.com/tgdigital-hub/whaleshark/internal/notify"
	"github.com/tgdigital-hub/whaleshark/internal/overlap"
	"github.com/tgdigital-hub/whaleshark/internal/panes"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
	"github.com/tgdigital-hub/whaleshark/internal/push"
	"github.com/tgdigital-hub/whaleshark/internal/question"
	"github.com/tgdigital-hub/whaleshark/internal/rules"
	"github.com/tgdigital-hub/whaleshark/internal/runtask"
	"github.com/tgdigital-hub/whaleshark/internal/store"
	"github.com/tgdigital-hub/whaleshark/internal/team"
	"github.com/tgdigital-hub/whaleshark/internal/term"
	"github.com/tgdigital-hub/whaleshark/internal/theme"
	"github.com/tgdigital-hub/whaleshark/internal/view"
	"github.com/tgdigital-hub/whaleshark/internal/worker"
)

// plugs is every package, lowest first: a package may use from the kit what
// an earlier one put there.
var plugs = []func(*contract.Kit){
	platform.Plug, rules.Plug, store.Plug, herdr.Plug, theme.Plug, term.Plug,
	guide.Plug, hook.Plug, notify.Plug, evidence.Plug, gitwt.Plug, integrate.Plug,
	overlap.Plug, inbox.Plug, view.Plug, runtask.Plug, launch.Plug, worker.Plug,
	question.Plug, mail.Plug, team.Plug, doctor.Plug, panes.Plug, dashweb.Plug,
	dash.Plug, push.Plug, connect.Plug, cli.Plug,
}

func main() {
	k := contract.NewKit()
	for _, plug := range plugs {
		plug(k)
	}
	os.Exit(k.Main(k, os.Args[1:], os.Stdout, os.Stderr))
}
