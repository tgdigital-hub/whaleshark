package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// commands are the two command lines, for the POSIX shell Claude Code runs
// its hooks in on every system.
func (s settings) commands() (status, gate string, err error) {
	bin, err := s.k.Platform.SelfPath()
	line := func(form string) string {
		return s.k.Platform.Quote(contract.ShellPosix, []string{bin, "hook", form})
	}
	return line(formStatus), line(formGate), err
}

// rewrite takes our entries out of a settings file's text and, given the two
// commands, puts them in; everything else is kept as it was written. had
// says the text already held exactly these, found that it held any of ours.
// A status line of the person's own in this file is never replaced.
func rewrite(data []byte, status, gate string) (out []byte, had, found bool, err error) {
	top, hooks := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	var pre, kept []json.RawMessage
	var line entry
	read := func(raw json.RawMessage, v any) {
		if err == nil && len(bytes.TrimSpace(raw)) > 0 {
			err = json.Unmarshal(raw, v)
		}
	}
	read(data, &top)
	read(top["hooks"], &hooks)
	read(hooks["PreToolUse"], &pre)
	read(top["statusLine"], &line)
	for _, raw := range pre {
		var g group
		if read(raw, &g); len(g.Hooks) == 1 && isOurs(g.Hooks[0].Command, formGate) {
			had, found = had || g.Hooks[0].Command == gate, true
			continue
		}
		kept = append(kept, raw)
	}
	if err != nil {
		return nil, false, false, err
	}
	theirs := line.Command != "" && !isOurs(line.Command, formStatus)
	had, found = had && (theirs || line.Command == status), found || line.Command != "" && !theirs
	if gate != "" {
		kept = append(kept, encode(group{"*", []entry{{"command", gate}}}))
	}
	set := func(m map[string]json.RawMessage, key string, v any, empty bool) {
		if delete(m, key); !empty {
			m[key] = encode(v)
		}
	}
	set(hooks, "PreToolUse", kept, len(kept) == 0)
	set(top, "hooks", hooks, len(hooks) == 0)
	if !theirs {
		set(top, "statusLine", entry{"command", status}, status == "")
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
	status, gate, err := s.commands()
	if err != nil {
		return setup, err
	}
	data, err := s.k.Platform.Read(setup.File)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return setup, err
	}
	out, had, _, err := rewrite(data, status, gate)
	switch {
	case err != nil:
		err = fmt.Errorf("%s: %w", setup.File, err)
	case had:
	case write:
		err = s.replace(setup.File, out)
	default:
		out, _, _, _ = rewrite(nil, status, gate)
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
	out, _, found, rerr := rewrite(data, "", "")
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
