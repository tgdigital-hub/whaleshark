package runtask

import (
	"bufio"
	"fmt"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// trusted is what trust answers with.
type trusted struct {
	Lines    []string `json:"lines"`
	Changed  bool     `json:"changed"`
	Approved bool     `json:"approved"`
}

// trust shows everything the project brings along that can run something or
// reach outside it, marks what is new since the last approval, and records
// the person's yes for exactly that. Under --json it only shows.
func trust(c *contract.Call) (any, error) {
	lines, err := contract.TrustLines(c.Root)
	if err != nil {
		return nil, err
	}
	was, err := contract.ReadTrust(c.Kit.Platform.Read, c.Root)
	if err != nil {
		return nil, err
	}
	out := trusted{Lines: lines, Changed: !slices.Equal(lines, was.Lines)}
	switch {
	case len(lines) == 0:
		fmt.Fprintln(c.Out, "This project brings nothing that needs your approval.")
		return out, nil
	case !out.Changed:
		out.Approved = true
		fmt.Fprintf(c.Out, "Nothing has changed since you approved this on %s.\n", was.At.Local().Format("2 January 2006"))
	}
	for _, line := range lines {
		fmt.Fprintln(c.Out, map[bool]string{true: "  ", false: "+ "}[slices.Contains(was.Lines, line)]+line)
	}
	for _, line := range was.Lines {
		if !slices.Contains(lines, line) {
			fmt.Fprintln(c.Out, "- "+line)
		}
	}
	if out.Approved || c.JSON {
		return out, nil
	}
	fmt.Fprint(c.Out, "This is what the project's own files would run or reach. Approve exactly this? Type yes: ")
	answer, _ := bufio.NewReader(c.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "yes" && a != "y" {
		return nil, refuse(contract.ExitFailed, "declined", "Nothing was approved; the project runs with the defaults.", "whaleshark trust")
	}
	out.Approved = true
	fmt.Fprintln(c.Out, "Approved.")
	return out, contract.WriteTrust(c.Kit.Platform, c.Root, lines, c.Now)
}
