// Command herdr stands in for the real herdr on the PATH of a test: it hands
// its command line to the fake herdr and prints what that answers.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
)

func main() {
	cwd, _ := os.Getwd()
	var answer testkit.FakeAnswer
	conn, err := net.Dial("unix", os.Getenv(testkit.EnvFake))
	if err == nil {
		if err = json.NewEncoder(conn).Encode(testkit.FakeCall{Args: os.Args[1:], Env: os.Environ(), Cwd: cwd}); err == nil {
			err = json.NewDecoder(conn).Decode(&answer)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake herdr:", err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, answer.Stdout)
	fmt.Fprint(os.Stderr, answer.Stderr)
	os.Exit(answer.Exit)
}
