// Command fakeagent is the agent of a scripted test. The engine's double
// starts it in a pane with the script as its first argument, and the real
// keeper under an agent kind's name. It prints its
// environment, waits like a real agent for its first prompt, which names the
// prompt file, and then plays the steps, each a real whaleshark command.
// What it prints is the pane's screen.
package main

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/engine"
	"github.com/tgdigital-hub/whaleshark/internal/platform"
)

// started is the state of an agent whose session has just begun.
const started = "started"

// option is the argument after one, as a real agent takes its options.
func option(name string) string {
	if i := slices.Index(os.Args, name); i >= 0 && i+1 < len(os.Args) {
		return os.Args[i+1]
	}
	return ""
}

// tell reports this agent's state as a real agent's hook does: to the
// keeper, by the event of the agent that means the state, with its session.
func tell(state string) {
	k := contract.NewKit()
	platform.Plug(k)
	pane, _ := contract.PaneOf(os.Getenv)
	event := map[string]string{started: "SessionStart", contract.StatusWorking: "UserPromptSubmit", contract.StatusIdle: "Stop",
		contract.StatusBlocked: "PermissionRequest"}[state]
	session := cmp.Or(option("--resume"), option("--session"), "session-of-"+pane)
	engine.New(k).Call(context.Background(), contract.WireCall{Op: contract.OpHook, Kind: event, Pane: pane, Session: session})
}

// words splits a step on spaces, keeping what is inside double quotes together.
func words(step string) []string {
	var out []string
	for i, part := range strings.Split(step, `"`) {
		if i%2 == 1 {
			out = append(out, part)
		} else {
			out = append(out, strings.Fields(part)...)
		}
	}
	return out
}

func main() {
	env := os.Environ()
	slices.Sort(env)
	fmt.Println(strings.Join(env, "\n"))
	fmt.Println("fakeagent: ready")

	// The real keeper starts an agent by its kind's name alone: the script
	// is in a file of the attempt's, and every moment of the session is told.
	script, real := "", os.Getenv(testkit.EnvScripts) != ""
	if data, err := os.ReadFile(filepath.Join(os.Getenv(testkit.EnvScripts), os.Getenv(contract.EnvAttempt))); real && err == nil {
		script = string(data)
	} else if !real && len(os.Args) > 1 {
		script = os.Args[1]
	}
	if real {
		tell(started)
	}
	lines := bufio.NewScanner(os.Stdin)
	if state := option("--state"); real && state != "" {
		// An agent of a prepared picture is in its state until it is told
		// something, which sets it to work as it does a real one; one that
		// was resumed is at its prompt.
		if option("--resume") == "" {
			time.Sleep(300 * time.Millisecond) // whoever started it is told it is ready first
			tell(state)
		}
		for lines.Scan() {
			tell(contract.StatusWorking)
		}
		forever()
	}
	prompt := ""
	if after, slow := strings.CutPrefix(strings.Split(script, " | ")[0], "slow-paste "); slow && real {
		seconds, _ := strconv.ParseFloat(after, 64)
		prompt = slowPrompt(time.Duration(seconds * float64(time.Second)))
	} else if lines.Scan() {
		prompt = lines.Text()
	} else {
		return
	}
	if real {
		tell(contract.StatusWorking)
	}
	go func() {
		for lines.Scan() {
			fmt.Println("fakeagent: told:", lines.Text())
		}
	}()
	// The prompt is "Read and follow <path>"; the result file and the token lie beside that file.
	dir := filepath.Dir(prompt[strings.LastIndex(prompt, " ")+1:])
	whaleshark := func(args ...string) string {
		cmd := exec.Command(os.Getenv(contract.EnvBin), args...)
		cmd.Stderr = os.Stdout
		out, err := cmd.Output()
		fmt.Printf("fakeagent: whaleshark %s: %s%v\n", strings.Join(args, " "), out, err)
		return string(out)
	}
	answer := ""
	for _, step := range strings.Split(script, " | ") {
		w := words(step)
		if len(w) == 0 {
			continue
		}
		arg := func(i int) string {
			if i < len(w) {
				return w[i]
			}
			return ""
		}
		switch w[0] {
		case "progress", "mail", "report":
			whaleshark(w...)
		case "ask":
			answer = whaleshark(w...)
		case "expect-answer":
			if !strings.Contains(answer, arg(1)) {
				fmt.Printf("fakeagent: expected the answer %q, got %q\n", arg(1), answer)
				os.Exit(1)
			}
		case "write-result":
			os.WriteFile(filepath.Join(dir, "result.md"), []byte("The result of a scripted agent.\n"), 0o600)
		case "report-with-token":
			os.WriteFile(filepath.Join(dir, "token"), []byte(arg(1)+"\n"), 0o600)
			whaleshark("report", "done", "reported with another token")
		case "slow-paste": // played before the prompt, where it can be
		case "sleep":
			seconds, _ := strconv.ParseFloat(arg(1), 64)
			time.Sleep(time.Duration(seconds * float64(time.Second)))
		case "die":
			os.Exit(0)
		case "hang":
			tell(contract.StatusIdle)
			forever()
		case "block":
			tell(contract.StatusBlocked)
			forever()
		case "edit":
			if file, err := os.OpenFile(arg(1), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintf(file, "a line by %s\n", os.Getenv(contract.EnvAttempt))
				file.Close()
			}
		case "commit":
			exec.Command("git", "add", "-A").Run()
			exec.Command("git", "commit", "-q", "-m", "work of "+os.Getenv(contract.EnvAttempt)).Run()
		case "merge":
			// What a worker does with a sync brief: where the two sides clash,
			// the lines of both are kept and git's marks taken out.
			if exec.Command("git", "merge", "-q", "--no-edit", arg(1)).Run() != nil {
				clashed, _ := exec.Command("git", "diff", "--name-only", "--diff-filter=U").Output()
				for _, file := range strings.Fields(string(clashed)) {
					data, _ := os.ReadFile(file)
					var both strings.Builder
					for line := range strings.Lines(string(data)) {
						if !strings.HasPrefix(line, "<<<<<<<") && !strings.HasPrefix(line, "=======") && !strings.HasPrefix(line, ">>>>>>>") {
							both.WriteString(line)
						}
					}
					os.WriteFile(file, []byte(both.String()), 0o600)
				}
				exec.Command("git", "add", "-A").Run()
				exec.Command("git", "commit", "-q", "--no-edit").Run()
			}
		default:
			fmt.Println("fakeagent: unknown step:", step)
			os.Exit(2)
		}
	}
	tell(contract.StatusIdle)
	forever()
}

// slowPrompt reads the first prompt as an agent on a loaded machine does:
// key by key, with a paste taken in and shown only a while after it came. An
// Enter that comes before then is dropped, as the real agent drops it; one
// in an empty box does nothing.
func slowPrompt(after time.Duration) string {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Println("fakeagent: no terminal to read keys from:", err)
		os.Exit(3)
	}
	defer term.Restore(fd, old)
	var text []byte
	var last time.Time
	for buf := make([]byte, 4096); ; {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			term.Restore(fd, old)
			os.Exit(0)
		}
		now, had := time.Now(), len(text)
		for _, b := range buf[:n] {
			switch {
			case b != '\r' && b != '\n':
				text, last = append(text, b), now
			case len(text) > 0 && now.Sub(last) >= after:
				fmt.Print("fakeagent: took the prompt\r\n")
				return string(text)
			case len(text) > 0:
				fmt.Print("fakeagent: an Enter came before the paste was taken in\r\n")
			}
		}
		if shown := string(text); len(text) > had {
			time.AfterFunc(after, func() { fmt.Print("fakeagent: > " + shown + "\r\n") })
		}
	}
}

// forever is an agent sitting at its prompt until its pane is closed.
func forever() {
	for {
		time.Sleep(time.Hour)
	}
}
