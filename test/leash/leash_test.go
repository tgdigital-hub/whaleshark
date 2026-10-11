package leash

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The test program is four programs: the tests, a test that ends badly in
// the way its surroundings name, a program that lives until it is ended
// and writes down, ten times a second, that it still does, and one that
// writes that down once and ends.
const (
	roleEnv = "WHALESHARK_TEST_LEASH_ROLE"
	beatEnv = "WHALESHARK_TEST_LEASH_BEAT"
)

func TestMain(m *testing.M) {
	if os.Getenv(roleEnv) == "once" {
		_ = os.WriteFile(os.Getenv(beatEnv), []byte("ran"), 0o600)
		os.Exit(0)
	}
	if os.Getenv(roleEnv) == "program" {
		for n := 0; ; n++ {
			_ = os.WriteFile(os.Getenv(beatEnv), []byte(strconv.Itoa(n)), 0o600)
			time.Sleep(100 * time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

func program(t *testing.T) (*exec.Cmd, func()) {
	t.Helper()
	cmd, let := Command(os.Args[0])
	cmd.Env = append(os.Environ(), roleEnv+"=program")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, let
}

// alive reports whether the program wrote within the last moments.
func alive(beat string) bool {
	was, _ := os.ReadFile(beat)
	time.Sleep(300 * time.Millisecond)
	now, err := os.ReadFile(beat)
	return err == nil && string(was) != string(now)
}

func await(t *testing.T, beat string, want bool, why string) {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); alive(beat) != want; {
		if time.Now().After(end) {
			t.Fatalf("%s: the program lives = %v", why, !want)
		}
	}
}

// Not a test of its own: the test below runs it as a test that ends badly.
// Its cleanup, which is all that ended a keeper before, never runs.
func TestATestThatEndsBadly(t *testing.T) {
	how := os.Getenv(roleEnv)
	if how == "" {
		t.Skip("run by TestTheProgramEndsHoweverTheTestDoes")
	}
	cmd, _ := program(t)
	t.Cleanup(func() { cmd.Process.Kill() })
	await(t, os.Getenv(beatEnv), true, "started")
	switch how {
	case "panic":
		go panic("in a goroutine that is not the test's")
	case "exit":
		os.Exit(3)
	}
	time.Sleep(time.Hour) // until the time limit, or the kill
}

func TestTheProgramEndsHoweverTheTestDoes(t *testing.T) {
	for _, how := range []string{"kill", "limit", "panic", "exit"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			beat := filepath.Join(t.TempDir(), "beat")
			limit := "1m"
			if how == "limit" {
				limit = "3s"
			}
			test := exec.Command(os.Args[0], "-test.run=^TestATestThatEndsBadly$", "-test.timeout="+limit)
			test.Env = append(os.Environ(), roleEnv+"="+how, beatEnv+"="+beat)
			if err := test.Start(); err != nil {
				t.Fatal(err)
			}
			await(t, beat, true, "before the test ends")
			if how == "kill" {
				test.Process.Kill()
			}
			if err := test.Wait(); err == nil {
				t.Fatal("the test that ends badly ended well")
			}
			await(t, beat, false, "after the test ended by "+how)
		})
	}
}

// What a test cannot hold it can have asked to end: the program of After
// runs once the test's process is gone or lets go, and not before; and the
// holder ends when its program has.
func TestAfterRunsWhenTheTestIsGone(t *testing.T) {
	beat := filepath.Join(t.TempDir(), "beat")
	t.Setenv(beatEnv, beat)
	t.Setenv(roleEnv, "once")
	cmd, let := After(os.Args[0])
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(beat); err == nil {
		t.Fatal("the program ran while the test still held the leash")
	}
	let()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the holder: %v", err)
	}
	if said, _ := os.ReadFile(beat); string(said) != "ran" {
		t.Errorf("the program did not run after the test let go: %q", said)
	}
}

// A test that ends well lets go itself, and Wait gives the program's end.
func TestLettingGoEndsTheProgram(t *testing.T) {
	beat := filepath.Join(t.TempDir(), "beat")
	t.Setenv(beatEnv, beat)
	cmd, let := program(t)
	await(t, beat, true, "started")
	let()
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	select {
	case <-ended:
	case <-time.After(2 * patience):
		t.Fatal("the holder did not end after its program")
	}
	await(t, beat, false, "after letting go")
}
