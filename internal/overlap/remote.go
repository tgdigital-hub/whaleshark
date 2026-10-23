package overlap

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// remoteWait is how long gh may take to list the open pull requests.
const remoteWait = 15 * time.Second

// Remote asks gh for the open pull requests and their files, and raises an
// overlap event for each that changes a file the task's --owns names. The
// task's own pull request, by its number or its branch, is not counted.
// Nothing is changed in git, and a gh that fails or is missing says nothing.
func (o scanner) Remote(root, run, task string) {
	k := o.k
	p, err := contract.ReadProjectFile(root)
	if err != nil || !p.Overlap.RemotePRs {
		return
	}
	s, err := k.Store.Read(root, run)
	var t *contract.Task
	if err == nil {
		t, err = k.Rules.FindTask(s, task)
	}
	if err != nil || len(t.Owns) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteWait)
	defer cancel()
	// #nosec G204 -- the program gh, with a list of arguments the tool built
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--state", "open", "--limit", "100", "--json", "number,headRefName,files")
	cmd.Dir = root
	out, err := cmd.Output()
	var prs []struct {
		Number int    `json:"number"`
		Head   string `json:"headRefName"`
		Files  []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err != nil || json.Unmarshal(out, &prs) != nil {
		return
	}
	var events []contract.Event
	for _, pr := range prs {
		if t.PR != nil && t.PR.Number == pr.Number || t.Worktree != nil && t.Worktree.Branch == pr.Head {
			continue
		}
		var files []string
		for _, f := range pr.Files {
			if owned(t.Owns, f.Path) && !slices.Contains(files, f.Path) {
				files = append(files, f.Path)
			}
		}
		if len(files) == 0 {
			continue
		}
		more := map[bool]string{true: " and " + strconv.Itoa(len(files)-1) + " more"}[len(files) > 1]
		events = append(events, contract.Event{Kind: "overlap", Task: t.ID,
			Text: fmt.Sprintf("the open pull request #%d (%s) changes %s%s, which %s's --owns names", pr.Number, pr.Head, files[0], more, t.ID),
			Data: map[string]any{"kind": "pr", "pr": pr.Number, "branch": pr.Head, "files": files}})
	}
	if len(events) > 0 {
		k.Store.Change(root, run, func(s *contract.State) error {
			for _, e := range events {
				k.Rules.Raise(s, e, contract.Now())
			}
			return nil
		})
	}
}
