package restore

import (
	"slices"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/screen"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// The most text of one pane the file holds, however long its lines are.
const most = 1 << 20

// kind is how one kind of agent continues a conversation: the option that
// takes the session's id, and the options of an earlier start that name a
// conversation themselves and give way to it, with a value and without.
type kind struct {
	option string
	valued []string
	bare   []string
}

// kinds has Claude Code's options from its own help. A kind that is not
// here has not been run and is not resumed: its pane comes back as a shell.
var kinds = map[string]kind{
	"claude": {"--resume", []string{"--resume", "-r", "--session-id"}, []string{"--continue", "-c", "--fork-session"}},
}

// plan decides what a pane of the file starts now. An agent with a session
// is started with the arguments it had and its option to resume, so its
// model and its hooks survive. A pane program of ours is started as it was.
// Anything else is a shell under the same id: an agent that cannot be
// resumed, whose record then says so, and a command that was run there
// once, which a restart must not run a second time.
func (p *Pane) plan() {
	k, known := kinds[p.Rec.Agent]
	switch {
	case known && p.Rec.Session != "" && len(p.Argv) > 0:
		p.Run = []string{p.Argv[0], k.option, p.Rec.Session}
		for i := 1; i < len(p.Argv); i++ {
			name, _, joined := strings.Cut(p.Argv[i], "=")
			switch {
			case slices.Contains(k.bare, name):
			case slices.Contains(k.valued, name):
				if !joined && i+1 < len(p.Argv) && !strings.HasPrefix(p.Argv[i+1], "-") {
					i++
				}
			default:
				p.Run = append(p.Run, p.Argv[i])
			}
		}
	case p.Rec.Agent == "" && len(p.Argv) > 3 && p.Argv[1] == "ui" && p.Argv[2] == "run":
		p.Run = p.Argv
	default:
		p.Shell()
	}
}

// Text is the last lines of a pane, the oldest first: what scrolled off its
// screen and then what it shows, at most keep lines. The caller holds
// whatever guards the screen.
func Text(s *screen.Screen, keep int) []string {
	var rows, out []string
	if shown := s.Picture().Text(); shown != "" {
		rows = strings.Split(shown, "\n")
	}
	for i, n, size := s.Lines()+len(rows)-1, s.Lines(), 0; i >= 0 && len(out) < keep; i-- {
		line := ""
		if i >= n {
			line = rows[i-n]
		} else {
			var b strings.Builder
			for _, c := range s.Line(i) {
				b.WriteString(c.Text)
			}
			line = strings.TrimRight(b.String(), " ")
		}
		if size += len(line) + 1; size > most {
			break
		}
		out = append(out, line)
	}
	slices.Reverse(out)
	return out
}

// Show writes a pane's old lines to its new screen, above a line that says
// when the restart was. It is called before the pane's program starts, so
// the lines scroll off like anything printed. They are text and nothing
// else: whatever could steer a terminal is taken out first.
func Show(s *screen.Screen, lines []string, at time.Time) {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(term.Clean(line) + "\r\n")
	}
	b.WriteString("\x1b[2m-- restarted " + at.Format("15:04") + " --\x1b[m\r\n")
	s.Write([]byte(b.String()))
}

// Shell makes a pane of a loaded file a shell after all, for a keeper whose
// start of Run failed: the program is gone, or the agent is.
func (p *Pane) Shell() {
	p.Rec.Agent, p.Rec.Name, p.Rec.Session, p.Argv, p.Run = "", "", "", nil, nil
}
