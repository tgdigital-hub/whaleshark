package contract

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The marker files of a task's notes folder, for scripts that watch it:
// READY once its worker reported it done, DONE once it was accepted, or
// merged where each task is its own pull request.
const (
	MirrorReady = "READY"
	MirrorDone  = "DONE"
)

// MirrorDir is the notes folder results.mirror_dir names for a task of a
// run, with the day the task was added for {date}; empty while unset.
func (p ProjectFile) MirrorDir(run string, t *Task) string {
	if p.Results.MirrorDir == "" || t == nil {
		return ""
	}
	return strings.NewReplacer("{date}", t.CreatedAt.Local().Format(time.DateOnly), "{run}", run, "{task}", t.ID).Replace(p.Results.MirrorDir)
}

// Mirror copies into a task's notes folder its brief, from the run's
// folder, and the result file and pull request body of its attempt where
// they are, then writes marker there unless it is empty. The folder lies
// outside the project and the run, so neither close nor run rm takes it.
func Mirror(p ProjectFile, runDir, run string, t *Task, attempt, marker string, now time.Time) error {
	dir := p.MirrorDir(run, t)
	if dir == "" {
		return nil
	} else if !filepath.IsAbs(dir) {
		return fmt.Errorf("results.mirror_dir in whaleshark.toml must be a full path, not %.60q", dir)
	}
	files := map[string]string{"brief.md": filepath.Join(runDir, filepath.FromSlash(t.Brief))}
	if attempt != "" {
		files["result.md"] = filepath.Join(AttemptDir(runDir, attempt), "result.md")
		files[PRBody] = filepath.Join(AttemptDir(runDir, attempt), PRBody)
	}
	err := os.MkdirAll(dir, 0o700)
	for name, from := range files {
		// #nosec G304 -- a file of the run's own folder
		data, rerr := os.ReadFile(from)
		switch {
		case err != nil || errors.Is(rerr, fs.ErrNotExist):
		case rerr != nil:
			err = rerr
		default:
			err = os.WriteFile(filepath.Join(dir, name), data, 0o600)
		}
	}
	if err != nil || marker == "" {
		return err
	}
	return os.WriteFile(filepath.Join(dir, marker), []byte(now.Format(time.RFC3339)+"\n"), 0o600)
}
