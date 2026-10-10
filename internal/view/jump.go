package view

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// look reads the run a command is about and builds its view, every card in
// it. A command that needs a run ends with exit 4 where there is none.
func look(c *contract.Call) (*contract.State, contract.ViewInput, *contract.View, error) {
	s, err := load(c)
	if errors.Is(err, contract.ErrNoRun) {
		err = missing("no_run", "No run is open in this project.")
	}
	if err != nil {
		return nil, contract.ViewInput{}, nil, err
	}
	in := input(c, s, contract.Swept{}, nil)
	in.All = true
	return s, in, c.Kit.View(in), nil
}

func cardOf(v *contract.View, task string) *contract.Card {
	for _, s := range v.Sections {
		for i := range s.Cards {
			if s.Cards[i].Task == task {
				return &s.Cards[i]
			}
		}
	}
	return nil
}

// wordOf is the word that stands beside a card's mark.
func wordOf(c contract.Card) string {
	if c.Word != "" {
		return c.Word
	}
	return string(c.Look)
}

// looked records that the person has looked at a task. Nobody else's look
// counts, and a mark that could not be written only stays a little longer.
func looked(c *contract.Call, task string) {
	if c.Caller.Kind == contract.Human {
		_ = c.Kit.Store.Change(c.Root, c.Run, func(s *contract.State) error {
			return c.Kit.Rules.Looked(s, task, c.Now)
		})
	}
}

// jump puts a task's tab in front, or the lead agent's. With no argument it
// lists the tabs there are and reads the number of one.
func jump(c *contract.Call) (any, error) {
	if len(c.Args) > 1 {
		return nil, usage("jump takes one task, or lead.")
	}
	s, in, v, err := look(c)
	if err != nil {
		return nil, err
	}
	target, open := contract.Lead, []string{contract.Lead}
	if len(c.Args) == 0 {
		fmt.Fprintf(c.Out, "%3d  %s\n", 0, leadName)
		for _, sec := range v.Sections {
			for _, card := range sec.Cards {
				if card.Tab != "" {
					fmt.Fprintf(c.Out, "%3d  %s%s\n", len(open), pad(clean(card.Name), 21), wordOf(card))
					open = append(open, card.Task)
				}
			}
		}
		n := 0
		if c.JSON || c.Stdin == nil {
			return open, nil
		} else if _, err := fmt.Fscan(c.Stdin, &n); err != nil {
			return open, nil
		} else if n < 0 || n >= len(open) {
			return nil, missing("no_such_tab", "There is no tab %d in the list.", n)
		}
		target = open[n]
	} else if !strings.EqualFold(c.Args[0], contract.Lead) {
		target = c.Args[0]
	}

	tabOf := map[string]string{}
	if in.Terms != nil {
		for _, p := range in.Terms.Panes {
			tabOf[p.ID] = p.Tab
		}
	}
	lead, name, tab := s.Run.Orchestrator, leadName, ""
	if target != contract.Lead {
		t, err := c.Kit.Rules.FindTask(s, target)
		if err != nil {
			return nil, err
		}
		target, name, tab = t.ID, clean(t.Name), cardOf(v, t.ID).Tab
	} else if lead != nil {
		tab = tabOf[lead.Pane]
	}
	if tab == "" {
		return nil, missing("no_tab", "%s has no open tab.", name)
	}
	if err := c.Kit.Terms.TabFocus(tab); err != nil {
		return nil, &contract.Refusal{Exit: contract.ExitEnv, Code: "no_focus", Message: "The tab of " + name + " could not be put in front: " + err.Error() + "."}
	}
	if target != contract.Lead {
		looked(c, target)
	} else {
		// Back to the conversation, should one of our own panes have the keys:
		// it lies above the action pane, or below one set on top, and left of
		// the fleet.
		for _, step := range [][2]string{{in.UI.ActionsPane, contract.Up}, {in.UI.ActionsPane, contract.Down}, {in.UI.FleetPane, contract.Left}} {
			if tabOf[step[0]] != tab {
				continue
			}
			if got, err := c.Kit.Terms.PaneFocus(step[0], step[1]); err != nil || got == lead.Pane {
				break
			}
		}
	}
	fmt.Fprintln(c.Out, "in front:", name)
	return tab, nil
}
