package guide

import (
	"bytes"
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Block is the rules block, marker line to marker line. It carries no
// version: it only points at the guide this program prints.
//
//go:embed block.md
var Block string

// A block is found by its two marker lines.
const (
	beginMark = "<!-- whaleshark:begin"
	endMark   = "<!-- whaleshark:end"
)

// AgentFile is the user-wide instruction file one agent tool reads: Dir and
// Name under the home folder, or Name in the folder Env names when it is set.
// Confirmed is true only for a row read in the tool's own page, Source.
type AgentFile struct {
	Kind, Dir, Name, Env string
	Confirmed            bool
	Source               string
}

// Files is the table. For a kind with no row the person pastes FirstMessage.
var Files = []AgentFile{
	{Kind: "claude", Dir: ".claude", Name: "CLAUDE.md", Confirmed: true, Source: "https://code.claude.com/docs/en/memory"},
	{Kind: "codex", Dir: ".codex", Name: "AGENTS.md", Env: "CODEX_HOME", Confirmed: true, Source: "https://developers.openai.com/codex/guides/agents-md"},
	{Kind: "gemini", Dir: ".gemini", Name: "GEMINI.md", Confirmed: true, Source: "https://google-gemini.github.io/gemini-cli/docs/cli/gemini-md.html"},
	{Kind: "opencode", Dir: ".config/opencode", Name: "AGENTS.md", Confirmed: true, Source: "https://opencode.ai/docs/rules/"},
}

// File returns a kind's instruction file, and false for a kind with no row.
func File(kind, home string, getenv func(string) string) (string, bool) {
	for _, f := range Files {
		if f.Kind != kind {
			continue
		}
		if dir := getenv(f.Env); f.Env != "" && dir != "" {
			return filepath.Join(dir, f.Name), true
		}
		return filepath.Join(home, filepath.FromSlash(f.Dir), f.Name), true
	}
	return "", false
}

// BlockState is what a file holds of the block.
type BlockState string

const (
	Absent BlockState = "absent"
	Ours   BlockState = "ours"   // exactly what this program writes
	Edited BlockState = "edited" // a marker is there and the text is not ours: never touched
)

// find returns where our exact block starts a line of data, or -1.
func find(data []byte) int {
	if bytes.HasPrefix(data, []byte(Block)) {
		return 0
	}
	if i := bytes.Index(data, []byte("\n"+Block)); i >= 0 {
		return i + 1
	}
	return -1
}

func state(data []byte) BlockState {
	switch {
	case find(data) >= 0:
		return Ours
	case bytes.Contains(data, []byte(beginMark)), bytes.Contains(data, []byte(endMark)):
		return Edited
	}
	return Absent
}

// read returns a file's content, and missing when there is no such file.
func read(file func(string) ([]byte, error), path string) (data []byte, missing bool, err error) {
	data, err = file(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	return data, false, err
}

// Look says what a file holds of the block; a missing file holds none.
func Look(path string) (BlockState, error) {
	data, _, err := read(os.ReadFile, path)
	return state(data), err
}

// Write puts the block at the end of a file, after one line break when the
// file has content, and makes the file when there is none. It returns what
// the file held before: anything but Absent means nothing was written. Only
// a block written here, onto Absent, is one Remove takes out byte for byte.
func Write(p contract.Platform, path string) (before BlockState, created bool, err error) {
	data, created, err := read(p.Read, path)
	if err != nil {
		return "", false, err
	}
	if before = state(data); before != Absent {
		return before, false, nil
	}
	if len(data) > 0 {
		data = append(data, '\n')
	}
	return Absent, created, save(p, path, append(data, Block...), created)
}

// Remove takes our block out again, with the line break Write put before
// it. An edited block is left alone and reported by the state returned. With
// created, a file left empty is deleted.
func Remove(p contract.Platform, path string, created bool) (before BlockState, err error) {
	data, _, err := read(p.Read, path)
	if err != nil {
		return "", err
	}
	if before = state(data); before != Ours {
		return before, nil
	}
	i := find(data)
	rest := append(data[:max(i-1, 0):max(i-1, 0)], data[i+len(Block):]...)
	if created && len(rest) == 0 {
		return Ours, os.Remove(path)
	}
	return Ours, save(p, path, rest, false)
}

// save replaces a file in one step, keeping its permissions and writing
// through a link to the file it points at.
func save(p contract.Platform, path string, data []byte, created bool) error {
	mode := fs.FileMode(0o600)
	if created {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
	} else {
		target, err := filepath.EvalSymlinks(path)
		info, statErr := os.Stat(path)
		if err = errors.Join(err, statErr); err != nil {
			return err
		}
		path, mode = target, info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".whaleshark-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	err = errors.Join(err, tmp.Chmod(mode), tmp.Close())
	if err == nil {
		err = p.Replace(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
