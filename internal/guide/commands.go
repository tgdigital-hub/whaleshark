// Package guide holds what agents are told, compiled in so that it matches
// this version: the two guides, the worker's prompt and the rules block.
package guide

import (
	_ "embed"
	"io"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

var (
	//go:embed lead.md
	Lead string
	//go:embed worker.md
	Worker string
)

// FirstMessage is pasted to a lead agent whose tool has no instruction file.
const FirstMessage = "Run `whaleshark guide lead` and follow it."

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Handle("guide", run) }

// run prints one guide: the one asked for, else the caller's own.
func run(c *contract.Call) (any, error) {
	which := "lead"
	if c.Caller.Kind == contract.Worker {
		which = "worker"
	}
	if len(c.Args) > 0 {
		which = c.Args[0]
	}
	text := map[string]string{"lead": Lead, "worker": Worker}[which]
	if text == "" || len(c.Args) > 1 {
		return nil, &contract.Refusal{Exit: contract.ExitUsage, Code: "usage",
			Message: "guide takes one of: worker, lead",
			Next:    []string{"whaleshark guide lead", "whaleshark guide worker"}}
	}
	if _, err := io.WriteString(c.Out, text); err != nil {
		return nil, err
	}
	return map[string]string{"guide": which, "text": text}, nil
}
