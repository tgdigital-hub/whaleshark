// Package cli is the front of every command: the parser, the caller rules,
// the one validator, the envelope and the exit codes, and help and version.
package cli

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Version is the version of the program; a release build sets it.
var Version = "dev"

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) {
	k.Main = Main
	k.Handle("help", help)
	k.Handle("version", version)
}

var callers = []struct {
	kind contract.CallerKind
	name string
}{
	{contract.Worker, "a worker"}, {contract.Orchestrator, "the lead agent"},
	{contract.Human, "the person"}, {contract.Unbound, "a tab not bound to a run"},
}

var exits = map[int]string{
	contract.ExitOK: "done", contract.ExitFailed: "failed", contract.ExitUsage: "usage error",
	contract.ExitEnv: "environment unusable", contract.ExitMissing: "not found", contract.ExitRefused: "refused",
	contract.ExitClash: "a pair conflicts", contract.ExitNoLand: "no longer lands", contract.ExitPending: "still open",
}

// row is one command as help --json prints it: the table itself.
type row struct {
	Name    string    `json:"name"`
	Section string    `json:"section"`
	Phase   int       `json:"phase"`
	Built   bool      `json:"built"`
	Who     []string  `json:"who"`
	Usage   string    `json:"usage"`
	Help    string    `json:"help"`
	Exits   []int     `json:"exits"`
	Flags   []flagRow `json:"flags,omitempty"`
	Subs    []flagRow `json:"subs,omitempty"`
	Example string    `json:"example,omitempty"`
}

// flagRow is a flag, or a form of a command with callers of its own.
type flagRow struct {
	Name  string   `json:"name"`
	Value string   `json:"value,omitempty"`
	Phase int      `json:"phase,omitempty"`
	Who   []string `json:"who,omitempty"`
}

func who(cmd *contract.Command, sub string) (kinds, words []string) {
	for _, c := range callers {
		if cmd.May(c.kind, sub) {
			kinds, words = append(kinds, string(c.kind)), append(words, c.name)
		}
	}
	if len(words) == len(callers) {
		words = []string{"everyone"}
	}
	return kinds, words
}

// rowOf describes a command for one caller: --human is shown to the person only.
func rowOf(c *contract.Call, cmd *contract.Command) row {
	r := row{Name: cmd.Name, Section: cmd.Section, Phase: cmd.Phase, Built: c.Kit.Handler(cmd.Name) != nil,
		Usage: cmd.Usage, Help: cmd.Help, Exits: cmd.Exits, Example: cmd.Example}
	r.Who, _ = who(cmd, "")
	human := c.Caller.Kind == contract.Human
	if !human {
		r.Example = strings.ReplaceAll(r.Example, " --human", "")
	}
	for _, f := range flagsOf(cmd) {
		if f.Name == "human" && !human {
			continue
		}
		fr := flagRow{Name: f.Name, Value: f.Value, Phase: f.Phase}
		if f.Who != 0 {
			fr.Who, _ = who(&contract.Command{Who: f.Who}, "")
		}
		r.Flags = append(r.Flags, fr)
	}
	for _, s := range cmd.Subs {
		kinds, _ := who(cmd, s.Name)
		r.Subs = append(r.Subs, flagRow{Name: s.Name, Phase: s.Phase, Who: kinds})
	}
	return r
}

// help prints the command table: every command on a line, or one in full.
func help(c *contract.Call) (any, error) {
	if len(c.Args) > 1 {
		return nil, usage(c.Command, "help takes one command at most.")
	}
	if len(c.Args) == 0 {
		rows := make([]row, len(contract.Commands))
		for i := range contract.Commands {
			rows[i] = rowOf(c, &contract.Commands[i])
			built := ""
			if !rows[i].Built {
				built = "  (" + contract.ErrNotBuilt.Error() + ")"
			}
			fmt.Fprintf(c.Out, "  %-9s %s%s\n", rows[i].Name, rows[i].Help, built)
		}
		return map[string]any{"commands": rows}, nil
	}
	cmd := contract.Find(c.Args[0])
	if cmd == nil {
		return nil, NoSuch("command", c.Args[0], names(), "whaleshark help")
	}
	r := rowOf(c, cmd)
	for form := range strings.SplitSeq(cmd.Usage, " / ") {
		fmt.Fprintf(c.Out, "whaleshark %s\n", form)
	}
	fmt.Fprintf(c.Out, "  %s\n\n", cmd.Help)
	line := func(label, text string) { fmt.Fprintf(c.Out, "%-9s %s\n", label, text) }
	_, words := who(cmd, "")
	line("For:", strings.Join(words, ", "))
	for _, s := range cmd.Subs {
		_, words := who(cmd, s.Name)
		line("", cmd.Name+" "+s.Name+": "+strings.Join(words, ", "))
	}
	label := "Flags:"
	for _, f := range r.Flags[:len(cmd.Flags)] {
		note := ""
		if f.Phase != 0 {
			note = "  (phase " + contract.PhaseName(f.Phase) + ")"
		}
		if f.Who != nil {
			note += "  (the person only)"
		}
		line(label, strings.TrimSpace("--"+f.Name+" "+f.Value)+note)
		label = ""
	}
	line("Always:", strings.Join(spell(contract.CommonFlags, c.Caller.Kind == contract.Human), ", "))
	var codes []string
	for _, e := range cmd.Exits {
		codes = append(codes, fmt.Sprintf("%d %s", e, exits[e]))
	}
	line("Exits:", strings.Join(codes, ", "))
	if r.Example != "" {
		line("Example:", r.Example)
	}
	if !r.Built {
		fmt.Fprintf(c.Out, "\nNot built yet: it comes with phase %s.\n", contract.PhaseName(cmd.Phase))
	}
	return r, nil
}

// version prints the version, the build and the herdr version seen.
func version(c *contract.Call) (any, error) {
	if len(c.Args) > 0 {
		return nil, usage(c.Command, "version takes no argument.")
	}
	v := struct {
		Version string `json:"version"`
		Build   string `json:"build"`
		Herdr   string `json:"herdr"`
	}{Version: Version, Build: "unknown"}
	if info, ok := debug.ReadBuildInfo(); ok {
		v.Build = info.GoVersion
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				v.Build = s.Value[:min(12, len(s.Value))] + " " + info.GoVersion
			}
		}
	}
	seen := "not seen"
	if h, err := c.Kit.Terms.Version(); err == nil {
		v.Herdr, seen = h, h
	}
	fmt.Fprintf(c.Out, "whaleshark %s (%s)\nherdr %s\n", v.Version, v.Build, Plain(seen))
	return v, nil
}
