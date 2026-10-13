package panes

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/term"
)

// save is `set --save`: the person's settings as one text file to carry to
// another computer, a new file for the login alone, and what was left out
// of it for them to set by hand.
func save(c *contract.Call, file string) (any, error) {
	p := c.Kit.Platform
	dirs, err := p.Dirs()
	if err != nil {
		return nil, err
	}
	pr, left, err := contract.SaveProfile(p, dirs)
	var b bytes.Buffer
	if err == nil {
		err = toml.NewEncoder(&b).Encode(pr)
	}
	if err == nil {
		// A new file only: nothing is replaced, and no link at that name is followed.
		var f *os.File
		if f, err = os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil { // #nosec G304 -- the path the person named
			_, err = f.Write(b.Bytes())
			err = cmp.Or(err, f.Close(), p.Private(file))
		}
	}
	if err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitRefused, Code: "not_written", Message: "Your settings were not saved, and nothing was replaced: " + err.Error() + "."}
	}
	fmt.Fprintln(c.Out, "saved: "+file)
	if len(left) > 0 {
		fmt.Fprintln(c.Out, "left out: "+strings.Join(left, ", "))
	}
	return map[string]any{"file": file, "left": left}, nil
}

// differs is one setting a profile would change here.
type differs struct {
	key            *contract.Key
	name, from, to string
}

// load is `set --load`: a profile read as data and held against what stands
// here. Every difference is shown, a limit taken away and a guard switched
// off with their warning; one typed yes sets the plain ones, and each line
// that runs a program is shown exactly and asked about by itself. Every
// change is contract.Set's, as the person: a value passes its key's own
// check there, and a tool's program is refused inside a project. What the
// file says is printed clean: it is somebody else's text.
func load(c *contract.Call, file string) (any, error) {
	p := c.Kit.Platform
	dirs, err := p.Dirs()
	if err != nil {
		return nil, err
	}
	data, err := p.Read(file)
	pr, skipped, rerr := contract.ReadProfile(data)
	if err = cmp.Or(err, rerr); err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage", Message: "That file cannot be read as saved settings, and nothing was changed: " + err.Error() + "."}
	}
	var plain, tools, runs []differs
	differ := func(k *contract.Key, name, to string) {
		d := differs{k, name, contract.Current(p, dirs, name), to}
		switch {
		case to == "" || to == d.from || k.Asks == contract.AsksShown:
		case k.Asks == contract.AsksRuns:
			runs = append(runs, d)
		case k.Section == contract.SecTools:
			tools = append(tools, d) // after its program: a tool is nothing without one
		default:
			plain = append(plain, d)
		}
	}
	for i := range contract.Keys {
		switch k := &contract.Keys[i]; {
		case k.Section == "" || k.Section == contract.SecTools && strings.HasSuffix(k.Name, ">"):
		case k.Section == contract.SecTools:
			for _, name := range slices.Sorted(maps.Keys(pr.Tools)) {
				t := pr.Tools[name]
				differ(k, strings.Replace(k.Name, "<name>", name, 1), k.Of(&t))
			}
		case strings.HasSuffix(k.Name, ">"):
			for _, e := range pr.Engine.Shortcuts {
				differ(k, "shortcut."+e.Key, strings.Join(e.Argv, " "))
			}
		default:
			// A key's place is in the one file it lies in: of a profile's
			// two that file answers it, and no other key has a place there.
			differ(k, k.Name, cmp.Or(k.Of(&pr.Config), k.Of(&pr.Engine)))
		}
	}
	say := func(text string) { fmt.Fprintln(c.Out, term.Clean(text)) }
	for _, name := range skipped {
		say("not known here, and skipped: " + name)
	}
	plain = append(plain, tools...)
	server, _ := contract.ReadServer(p.Peek)
	for _, d := range plain {
		say("  " + d.key.Label + " (" + d.name + "): " + cmp.Or(d.from, "not set") + " → " + d.to)
		for _, line := range d.key.UnlimitedWarning(0, server.Cap(contract.Me()), pr.Config.Spend.Guard) {
			if d.to == "unlimited" {
				fmt.Fprintln(c.Out, "      "+line)
			}
		}
		if d.key.Section == contract.SecGuards && d.to == contract.Off {
			fmt.Fprintln(c.Out, "      This switches a guard off.")
		}
	}
	done := []string{}
	if c.JSON || len(plain)+len(runs) == 0 {
		fmt.Fprintln(c.Out, "Nothing here differs from that file.")
		return map[string]any{"changed": done, "skipped": skipped}, nil
	}
	in, quiet := bufio.NewReader(c.Stdin), *c
	quiet.Err = nil // the warnings were shown above, before the yes
	yes := func(ask string) bool {
		fmt.Fprint(c.Out, term.Clean(ask)+" Type yes: ")
		answer, _ := in.ReadString('\n')
		return strings.EqualFold(strings.TrimSpace(answer), "yes")
	}
	set := func(d differs) {
		if _, err := contract.Set(&quiet, d.name, d.to); err != nil {
			say("not set: " + d.name + ": " + err.Error())
		} else {
			done = append(done, d.name)
		}
	}
	ok := len(plain) > 0 && yes(fmt.Sprintf("Make these %d changes?", len(plain)))
	for _, d := range runs {
		line := d.name + " runs: " + d.to
		if words := strings.Fields(d.to); len(words) > 0 {
			if _, err := exec.LookPath(words[0]); err != nil {
				line += " · that program is not on this computer"
			}
		}
		if yes(line + " · use exactly this?") {
			set(d)
		}
	}
	for _, d := range plain {
		if ok {
			set(d)
		}
	}
	fmt.Fprintf(c.Out, "%d settings changed.\n", len(done))
	return map[string]any{"changed": done, "skipped": skipped}, nil
}
