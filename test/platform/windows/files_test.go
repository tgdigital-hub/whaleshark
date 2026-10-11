package windows

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// held has another program, PowerShell, open a file and keep it open with
// the leave it gives everybody else, by .NET's names for it. What comes
// back lets the file go.
func held(t *testing.T, path, share string) (release func()) {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded(
		"$f = [IO.File]::Open('"+path+"', 'Open', 'Read', '"+share+"'); 'held'; [Console]::In.ReadLine() | Out-Null; $f.Close()"))
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var said bytes.Buffer
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = &said
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	release = sync.OnceFunc(func() { in.Close(); cmd.Wait() })
	t.Cleanup(release)
	if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "held" {
		release()
		t.Fatalf("the other program could not open %s (leave %q): %q %s", path, share, line, &said)
	}
	return release
}

// folder is every file under a folder with what it holds.
func folder(t *testing.T, dir string) map[string]string {
	t.Helper()
	all := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			var data []byte
			data, err = os.ReadFile(path)
			all[path] = string(data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// TestARecordAnotherProgramHoldsOpen: while another program holds a run's
// record open without leave to replace it, a command that would change the
// run waits its two seconds, ends with state_busy and leaves every file of
// the run as it was; once the program lets go, the same command goes
// through. A program that gave that leave stands in nobody's way.
func TestARecordAnotherProgramHoldsOpen(t *testing.T) {
	onlyWindows(t)
	p := evening(t)
	_, run := p.Record()
	for i, c := range []struct {
		share string
		busy  bool
	}{{"None", true}, {"Read", true}, {"ReadWrite", true}, {"ReadWrite, Delete", false}} {
		before, text := folder(t, run), fmt.Sprintf("held with leave %d", i)
		// Made before the file is held: making a command reads the record.
		cmd, again := p.Command(worker, "progress", "--json", "70", text), p.Command(worker, "progress", "71", text)
		release := held(t, filepath.Join(run, "state.json"), c.share)
		began := time.Now()
		out, err := cmd.CombinedOutput()
		took := time.Since(began).Round(100 * time.Millisecond)
		release()
		t.Logf("FACT held open with leave %q: the command took %v and ended: %v", c.share, took, err)
		if !c.busy {
			if err != nil || note(p) != text {
				t.Errorf("leave %q: the command should have gone through: %v, the record holds %q\n%s", c.share, err, note(p), out)
			}
			continue
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != contract.ExitEnv || !strings.Contains(string(out), "state_busy") || took < 2*time.Second {
			t.Errorf("leave %q: want exit %d and state_busy after two seconds, got %v after %v\n%s", c.share, contract.ExitEnv, err, took, out)
		}
		after := folder(t, run)
		for path, was := range before {
			if is, ok := after[path]; !ok || is != was {
				t.Errorf("leave %q: %s was changed by a command that was refused", c.share, path)
			}
		}
		for path := range after {
			if _, ok := before[path]; !ok {
				t.Errorf("leave %q: the refused command left %s behind", c.share, path)
			}
		}
		if out, err := again.CombinedOutput(); err != nil || note(p) != text {
			t.Errorf("leave %q, let go: the command should go through now: %v, the record holds %q\n%s", c.share, err, note(p), out)
		}
	}
}

// TestEveryTabAtOnce runs a real command from every tab that is at work at the same
// moment, five times over, against the one record. Every command must end
// well, and after each round every tab's word must be the one of that
// round: a lock lost or a replace over another's work would leave one tab
// with an older word.
func TestEveryTabAtOnce(t *testing.T) {
	p := evening(t)
	before, _ := p.Record()
	var workers []string
	for id, a := range before.Attempts {
		if a.State == contract.AttemptWorking {
			workers = append(workers, id)
		}
	}
	for round := range 5 {
		text := fmt.Sprintf("round %d", round)
		var all sync.WaitGroup
		for _, id := range workers {
			cmd := p.Command(id, "progress", fmt.Sprint(10+round), text)
			all.Add(1)
			go func() {
				defer all.Done()
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s: %v\n%s", id, err, out)
				}
			}()
		}
		all.Wait()
		after, _ := p.Record()
		for _, id := range workers {
			if got := after.Attempts[id].Progress.Note; got != text {
				t.Errorf("%s holds %q after %q", id, got, text)
			}
		}
	}
	t.Logf("FACT %d tabs at once, five rounds: nothing lost", len(workers))
}

// TestACommandsChangeIsTold: a real command's change of the record reaches
// a watcher of this process by a notice of the system's, not by the look
// once a second that stands in where notices fail.
func TestACommandsChangeIsTold(t *testing.T) {
	p := evening(t)
	_, run := p.Record()
	w, err := p.Kit.Platform.Watch(filepath.Join(run, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := range 5 {
		// Whatever is still on its way from the change before is not this one's.
		for quiet := false; !quiet; {
			select {
			case <-w.Changes():
			case <-time.After(300 * time.Millisecond):
				quiet = true
			}
		}
		if out, err := p.Command(worker, "progress", fmt.Sprint(20+i), "told").CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		began := time.Now()
		select {
		case <-w.Changes():
			t.Logf("FACT a command's change was told %v after the command ended", time.Since(began).Round(time.Millisecond))
		case <-time.After(900 * time.Millisecond):
			t.Errorf("change %d was not told within the second the slow look takes", i)
		}
	}
	if w.Slow() {
		t.Error("the watcher gave the system's notices up")
	}
}
