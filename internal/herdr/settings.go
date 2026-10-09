package herdr

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// settingsPath is where herdr 0.9.1 looks for its settings on a Mac and on Linux.
func (a *Adapter) settingsPath() string {
	if a.Settings != "" {
		return a.Settings
	}
	if path := os.Getenv("HERDR_CONFIG_PATH"); path != "" {
		return path
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "herdr", "config.toml")
}

// delivery is herdr's pop-up setting: "off" (herdr's default), "herdr",
// "terminal" or "system".
func (a *Adapter) delivery() (string, error) {
	var f struct {
		UI struct{ Toast struct{ Delivery string } }
	}
	_, err := toml.DecodeFile(a.settingsPath(), &f)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if f.UI.Toast.Delivery == "" {
		return "off", nil
	}
	return f.UI.Toast.Delivery, nil
}

// Our entries sit between these two lines, at the end of the file.
const (
	keysBegin = "# whaleshark shortcuts: begin (written by whaleshark; what is changed between these two lines is lost)"
	keysEnd   = "# whaleshark shortcuts: end"
)

// withoutKeys takes our lines out again, leaving every other byte as it was.
func withoutKeys(file string) string {
	i, j := strings.Index(file, keysBegin), strings.Index(file, keysEnd)
	if i < 0 || j < i {
		return file
	}
	if j += len(keysEnd); strings.HasPrefix(file[j:], "\n") {
		j++
	} else if i > 0 {
		i-- // the line end withKeys put before a block that ends the file
	}
	return file[:i] + file[j:]
}

func withKeys(file string, entries []contract.KeyEntry, quote func(string, []string) string) (string, error) {
	if len(entries) == 0 {
		return file, nil
	}
	block := keysBegin + "\n"
	for _, e := range entries {
		block += "[[keys.command]]\n"
		for _, kv := range [][2]string{{"key", e.Key}, {"type", e.Type}, {"command", quote("", e.Argv)}} {
			if strings.ContainsFunc(kv[1], unicode.IsControl) {
				return "", &Error{Failed, "a shortcut holds a control character"}
			}
			block += kv[0] + " = " + strconv.Quote(kv[1]) + "\n"
		}
	}
	block += keysEnd
	if file == "" || strings.HasSuffix(file, "\n") {
		return file + block + "\n", nil
	}
	return file + "\n" + block, nil
}

// SetKeys replaces our marked entries in herdr's settings file, and with no
// entries removes them. A file with entries is given to herdr's own check
// before it takes the old one's place, because herdr falls back to its
// defaults for a file it cannot read and drops a shortcut whose key the
// person has bound already; a fault the file had before is not held against
// it. Then the running server is told to read the file.
func (a *Adapter) SetKeys(entries []contract.KeyEntry) error {
	path := a.settingsPath()
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	data, err := a.kit.Platform.Read(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file, err := withKeys(withoutKeys(string(data)), entries, a.kit.Platform.Quote)
	if err != nil || file == string(data) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "config.*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.WriteString(file); err == nil {
		err = tmp.Chmod(mode)
	}
	if failed := tmp.Close(); err == nil {
		err = failed
	}
	check := func(file string) error {
		_, err := a.text(context.Background(), wait, []string{"HERDR_CONFIG_PATH=" + file}, "config", "check")
		return err
	}
	if err == nil && len(entries) > 0 {
		if err = check(tmp.Name()); Code(err) == Failed {
			if was := check(path); was != nil && was.Error() == err.Error() {
				err = nil
			}
		}
	}
	if err == nil {
		err = a.kit.Platform.Replace(tmp.Name(), path)
	}
	if err != nil || a.Settings != "" {
		return err
	}
	if err = a.do("server", "reload-config"); Code(err) == Unreachable {
		return nil
	}
	return err
}
