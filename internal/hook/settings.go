package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const claude = "claude"

// settings is the agent-settings provider. Claude Code is the one kind it
// knows how to set: every other gets nothing and is not gated.
type settings struct{ k *contract.Kit }

// What Claude Code's settings hold for us, as its manual gives them: a
// status line is one command, and what runs before a tool call is a list of
// groups of commands, each group with the tools it is for.
type entry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type group struct {
	Matcher string  `json:"matcher"`
	Hooks   []entry `json:"hooks"`
}

// localFile is the settings file Claude Code reads for one folder and one
// person, and keeps out of git.
func localFile(folder string) string {
	return filepath.Join(folder, ".claude", "settings.local.json")
}

func isOurs(command, form string) bool { return strings.HasSuffix(command, " hook "+form) }

// stateEvents are the moments of a session at which the harness runs `hook
// state`, by the names of Claude Code's manual; the gate runs at gateEvent.
var stateEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse",
	"PostToolUseFailure", "PermissionDenied", "Stop", "StopFailure", "SessionEnd"}

const gateEvent = "PreToolUse"

// line gives the command line of a form, for the POSIX shell Claude Code
// runs its hooks in on every system.
func (s settings) line() (func(form string) string, error) {
	bin, err := s.k.Platform.SelfPath()
	return func(form string) string {
		return s.k.Platform.Quote(contract.ShellPosix, append([]string{bin, "hook"}, strings.Fields(form)...))
	}, err
}

// rewrite takes our entries out of a settings file's text and, given the
// command lines, puts them in; everything else is kept as it was written.
// had says the text already held exactly these, found that it held any of
// ours. A status line of the person's own in this file is never replaced.
func rewrite(data []byte, line func(form string) string) (out []byte, had, found bool, err error) {
	top, hooks := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	var status entry
	read := func(raw json.RawMessage, v any) {
		if err == nil && len(bytes.TrimSpace(raw)) > 0 {
			err = json.Unmarshal(raw, v)
		}
	}
	set := func(m map[string]json.RawMessage, key string, v any, empty bool) {
		if delete(m, key); !empty {
			m[key] = encode(v)
		}
	}
	read(data, &top)
	read(top["hooks"], &hooks)
	read(top["statusLine"], &status)
	want := func(form string) string {
		if line == nil {
			return ""
		}
		return line(form)
	}
	had = line != nil
	for _, event := range stateEvents {
		forms := []string{formState + " " + event}
		if event == gateEvent {
			forms = []string{formGate, forms[0]}
		}
		var list, kept []json.RawMessage
		read(hooks[event], &list)
		held := 0
		for _, raw := range list {
			var g group
			if read(raw, &g); len(g.Hooks) == 1 && slices.ContainsFunc(forms, func(f string) bool { return isOurs(g.Hooks[0].Command, f) }) {
				if found = true; slices.ContainsFunc(forms, func(f string) bool { return g.Hooks[0].Command == want(f) }) {
					held++
				}
				continue
			}
			kept = append(kept, raw)
		}
		had = had && held == len(forms)
		for _, f := range forms {
			if line != nil {
				kept = append(kept, encode(group{"*", []entry{{"command", line(f)}}}))
			}
		}
		set(hooks, event, kept, len(kept) == 0)
	}
	if err != nil {
		return nil, false, false, err
	}
	theirs := status.Command != "" && !isOurs(status.Command, formStatus)
	had, found = had && (theirs || status.Command == want(formStatus)), found || status.Command != "" && !theirs
	set(top, "hooks", hooks, len(hooks) == 0)
	if !theirs {
		set(top, "statusLine", entry{"command", want(formStatus)}, line == nil)
	}
	return encode(top), had, found, nil
}

// encode writes JSON as a person would: indented, and with the characters
// of a shell line left as they are.
func encode(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return b.Bytes()
}

func (s settings) Ensure(kind, folder, scratch string, write bool) (contract.AgentSetup, error) {
	if kind != claude {
		return contract.AgentSetup{}, nil
	}
	setup := contract.AgentSetup{File: localFile(folder), Gated: contract.Gates[kind].Holds}
	line, err := s.line()
	if err != nil {
		return setup, err
	}
	data, err := s.k.Platform.Read(setup.File)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return setup, err
	}
	out, had, _, err := rewrite(data, line)
	switch {
	case err != nil:
		err = fmt.Errorf("%s: %w", setup.File, err)
	case had:
	case write:
		err = s.replace(setup.File, out)
	default:
		out, _, _, _ = rewrite(nil, line)
		file := filepath.Join(scratch, "claude-settings.json")
		setup.File, setup.Args = "", []string{"--settings", file}
		err = os.WriteFile(file, out, 0o600)
	}
	return setup, err
}

func (s settings) Remove(kind, folder string) error {
	if kind != claude {
		return nil
	}
	data, err := s.k.Platform.Read(localFile(folder))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	out, _, found, rerr := rewrite(data, nil)
	if err = errors.Join(err, rerr); err != nil || !found {
		return err
	}
	return s.replace(localFile(folder), out)
}

// replace moves a finished file over the person's settings file, which
// their agent may be reading or writing at that moment.
func (s settings) replace(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return s.k.Platform.Replace(tmp.Name(), path)
}
