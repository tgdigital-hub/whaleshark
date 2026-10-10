package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Trust is what a person last approved of one project with the trust
// command. Lines is what they were shown and Hash the hash of exactly that.
type Trust struct {
	Versioned
	Hash  string    `json:"hash"`
	Lines []string  `json:"lines"`
	At    time.Time `json:"at"`
}

// ProjectDir is the project's folder of ours, and TeamsDir the folder of the
// team files that come with a project.
const (
	ProjectDir = ".whaleshark"
	TeamsDir   = "whaleshark-teams"
)

// LoginDirs names the login's own folders; the platform sets it. The
// approvals are kept in the state folder, a file a project, and none in a
// project: a folder there can come along with a repository, and an approval
// found in it would be the repository's own word.
var LoginDirs = func() (Dirs, error) { return Dirs{}, errors.New("the login has no folder to keep an approval in") }

func trustFile(root string) (string, error) {
	dirs, err := LoginDirs()
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return filepath.Join(dirs.State, "trust", hex.EncodeToString(sum[:8])+".json"), err
}

// TrustLines is everything a project brings along that can run something or
// reach outside it, one line a thing, as trust shows it: the keys of
// whaleshark.toml that differ from the defaults, and each team file with the
// hash of its content. No lines means there is nothing to approve.
func TrustLines(root string) ([]string, error) {
	p, err := readProject(root)
	if err != nil {
		return nil, err
	}
	return trustLines(root, p)
}

func trustLines(root string, p ProjectFile) ([]string, error) {
	d, lines := ProjectDefaults(), []string{}
	add := func(key string, value, usual any) {
		if v := fmt.Sprint(value); v != fmt.Sprint(usual) {
			lines = append(lines, key+" = "+v)
		}
	}
	add("worktrees.dir", p.Worktrees.Dir, d.Worktrees.Dir)
	add("worktrees.share", p.Worktrees.Share, d.Worktrees.Share)
	add("setup.script", p.Setup.Script, "")
	add("setup.script_windows", p.Setup.ScriptWindows, "")
	add("agent.args", p.Agent.Args, d.Agent.Args)
	add("check.timeout_seconds", p.Check.TimeoutSeconds, d.Check.TimeoutSeconds)
	add("land.who", p.Land.Who, d.Land.Who)
	add("land.check", p.Land.Check, "")
	teams, _ := filepath.Glob(filepath.Join(root, TeamsDir, "*.toml"))
	slices.Sort(teams)
	for _, file := range teams {
		// #nosec G304 -- a team file of the project the command was run in
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		lines = append(lines, TeamsDir+"/"+filepath.Base(file)+" "+TokenHash(string(data)))
	}
	// A team's briefs lie beside it and become prompts: approved with it.
	// A link is not followed; its name alone is in the lines.
	if len(teams) == 0 {
		return lines, nil
	}
	dir := filepath.Join(root, TeamsDir)
	err := filepath.WalkDir(dir, func(file string, e fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(dir, file)
		if err != nil || e.IsDir() || filepath.Dir(rel) == "." && filepath.Ext(rel) == ".toml" {
			return err
		}
		var data []byte
		if e.Type().IsRegular() {
			// #nosec G304 G122 -- a brief of the project the command was run in
			if data, err = os.ReadFile(file); err != nil {
				return err
			}
		}
		lines = append(lines, TeamsDir+"/"+filepath.ToSlash(rel)+" "+TokenHash(string(data)))
		return nil
	})
	return lines, err
}

func trustHash(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// ReadTrust returns what was last approved; nothing yet is no error.
func ReadTrust(read func(path string) ([]byte, error), root string) (Trust, error) {
	var t Trust
	file, err := trustFile(root)
	if err == nil {
		err = ReadVersioned(read, file, FileVersion, &t)
	}
	return t, err
}

// WriteTrust records that a person approved exactly these lines.
func WriteTrust(p Files, root string, lines []string, now time.Time) error {
	file, err := trustFile(root)
	if err != nil {
		return err
	}
	return WriteVersioned(p, file, FileVersion, Trust{Versioned{FileVersion}, trustHash(lines), lines, now})
}

// held puts every key that needs approval back to its default when the
// project as it stands is not what was approved, and names what it held.
func held(root string, p *ProjectFile) error {
	lines, err := trustLines(root, *p)
	if err != nil || len(lines) == 0 {
		return err
	}
	if t, err := ReadTrust(os.ReadFile, root); err == nil && t.Hash == trustHash(lines) {
		return nil
	}
	d := ProjectDefaults()
	p.Worktrees.Dir, p.Worktrees.Share, p.Setup.Script, p.Setup.ScriptWindows = d.Worktrees.Dir, nil, "", ""
	p.Agent.Args, p.Check.TimeoutSeconds, p.Land = nil, d.Check.TimeoutSeconds, d.Land
	for _, line := range lines {
		name, _, _ := strings.Cut(line, " ")
		p.Held = append(p.Held, name)
	}
	return nil
}
