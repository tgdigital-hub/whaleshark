// Package guide holds what agents are told, compiled in so that it matches
// this version: the four guides, the two prompts and the rules block.
package guide

import (
	"cmp"
	_ "embed"
	"io"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var (
	//go:embed lead.md
	Lead string
	//go:embed worker.md
	Worker string
	//go:embed leader.md
	Leader string
	//go:embed orchestrator.md
	Orchestrator string
	//go:embed tools.tmpl
	toolsText string
	//go:embed guards.tmpl
	guardsText string
)

// FirstMessage is pasted to a lead agent whose tool has no instruction file.
const FirstMessage = "Run `whaleshark guide lead` and follow it."

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Handle("guide", run)
	k.SyncText = Sync
}

// run prints one guide: the one asked for, else the caller's own. A leader
// asking for the lead agent's gets its own; in Fleet Mode, the open run's or
// else the person's for the next, the lead agent's is the orchestrator's.
func run(c *contract.Call) (any, error) {
	which := cmp.Or(strings.Join(c.Args, " "), map[contract.CallerKind]string{contract.Worker: "worker"}[c.Caller.Kind], "lead")
	if c.Caller.Kind == contract.Leader && which == "lead" {
		which = "leader"
	}
	text := map[string]string{"lead": Lead, "worker": Worker, "leader": Leader}[which]
	if text == "" {
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage",
			Message: "guide takes one of: worker, lead, leader",
			Next:    []string{"whaleshark guide lead", "whaleshark guide worker", "whaleshark guide leader"}}
	}
	if d, err := c.Kit.Platform.Dirs(); err == nil && which != "worker" {
		person, _ := contract.ReadPerson(c.Kit.Platform.Peek, d.Config)
		fleets := person.Fleets.Mode == contract.On
		if s, err := c.Kit.Store.Read(c.Root, c.Run); c.Run != "" && err == nil && s.Run.ClosedAt.IsZero() {
			fleets = s.Run.Mode == contract.ModeFleets
		}
		if which == "lead" && fleets {
			which, text = "orchestrator", Orchestrator
		}
		tools, _ := contract.Tools(c.Kit.Platform, d)
		text += Guards(which, person) + Tools(which, tools, person)
		if saved, _ := contract.FleetsShown(c.Kit.Platform.Peek, c.Root, c.Caller.Kind); which == "orchestrator" && len(saved) > 0 {
			text += "\n## Fleets the person keeps here\nPlace work by what each works on; a thing that fits none gets a fleet made on the spot.\n"
			for _, f := range saved {
				text += strings.TrimSuffix("- "+cli.Line(f.Name)+": "+cli.Line(f.Purpose), ": ") + "\n"
			}
			if saved[0].Purpose == "" {
				text += "What each works on is shown once the person has approved what this project brings: `whaleshark trust`. Ask them what belongs where.\n"
			}
		}
	}
	if _, err := io.WriteString(c.Out, text); err != nil {
		return nil, err
	}
	return map[string]string{"guide": which, "text": text}, nil
}

// Guards is what a guide says after its own text: of each guardrail of the
// person's while it is on, so that one never switched on is never read of,
// and, to the agent the person talks to, of their settings. who is the reader.
func Guards(who string, person contract.PersonConfig) string {
	return render(guardsText, map[string]any{"Who": who, "Person": person})
}

// Tools is what a guide ends with, and a leader's prompt holds, where more
// than one agent tool is wired in with its program found: each with its rank,
// what is known of it and what the person says it suits. who is the reader.
func Tools(who string, tools []contract.Tool, person contract.PersonConfig) string {
	tools = slices.DeleteFunc(slices.Clone(tools), func(t contract.Tool) bool { return !t.Found() })
	return render(toolsText, map[string]any{"Who": who, "Tools": tools, "Person": person})
}
