// Command fakeagent is the agent of a scripted test. The fake herdr starts
// it in a pane with the script as its first argument. It prints its
// environment, waits like a real agent for its first prompt, which names the
// prompt file, and then plays the steps, each a real whaleshark command.
// What it prints is the pane's screen.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

// tell reports this agent's state to the fake herdr, as a real agent's hook does to herdr.
func tell(args ...string) {
	conn, err := net.Dial("unix", os.Getenv(testkit.EnvFake))
	if err != nil {
		return
	}
	defer conn.Close()
	args = append([]string{"pane", args[0], os.Getenv(contract.EnvPane), "--source", "fakeagent", "--agent", "claude"}, args[1:]...)
	if json.NewEncoder(conn).Encode(testkit.FakeCall{Args: args}) == nil {
		json.NewDecoder(conn).Decode(new(testkit.FakeAnswer))
	}
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

	lines := bufio.NewScanner(os.Stdin)
	if !lines.Scan() {
		return
	}
	prompt := lines.Text()
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
	for _, step := range strings.Split(os.Args[1], " | ") {
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
		case "sleep":
			seconds, _ := strconv.ParseFloat(arg(1), 64)
			time.Sleep(time.Duration(seconds * float64(time.Second)))
		case "die":
			os.Exit(0)
		case "hang":
			tell("report-agent", "--state", contract.StatusIdle)
			forever()
		case "block":
			tell("report-agent", "--state", contract.StatusBlocked)
			forever()
		case "edit":
			if file, err := os.OpenFile(arg(1), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintf(file, "a line by %s\n", os.Getenv(contract.EnvAttempt))
				file.Close()
			}
		case "commit":
			exec.Command("git", "add", "-A").Run()
			exec.Command("git", "commit", "-q", "-m", "work of "+os.Getenv(contract.EnvAttempt)).Run()
		default:
			fmt.Println("fakeagent: unknown step:", step)
			os.Exit(2)
		}
	}
	tell("report-agent", "--state", contract.StatusIdle)
	forever()
}

// forever is an agent sitting at its prompt until its pane is closed.
func forever() {
	for {
		time.Sleep(time.Hour)
	}
}
