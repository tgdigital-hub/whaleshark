package gitwt

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	includeFile = ".worktreeinclude"
	// copyBudget is the most a new worktree is given as copies. Anything
	// larger is linked, with [worktrees] share.
	copyBudget = 100 << 20
)

// carried is what a new worktree gets that git does not carry: the paths
// .worktreeinclude names, one a line, to copy, and those of the approved
// [worktrees] share to link. Both hold only what exists in the project and
// git ignores there. A path that could leave the project is refused whole,
// before anything is made; so is more to copy than the budget.
func (t *trees) carried(root string, p contract.ProjectFile) (copies, links []string, err error) {
	keep := func(from, path string, missing bool) (bool, error) {
		if err := cli.Valid("path", path, root); err != nil {
			return false, refuse("outside_project", "%s: %v.", from, err)
		}
		if _, err := os.Lstat(filepath.Join(root, path)); err != nil {
			if missing {
				fmt.Fprintf(t.warn, "%s: %s is not in the project and is left out.\n", from, cli.Plain(path))
			}
			return false, nil
		}
		if _, err := git(root, "check-ignore", "-q", "--", path); err != nil {
			fmt.Fprintf(t.warn, "%s: %s is not ignored by git and is left out.\n", from, cli.Plain(path))
			return false, nil
		}
		return true, nil
	}
	data, _ := t.k.Platform.Peek(filepath.Join(root, includeFile))
	var size int64
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimSpace(line); line == "" || line[0] == '#' {
			continue
		}
		ok, err := keep(includeFile, line, false)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		copies = append(copies, line)
		filepath.WalkDir(filepath.Join(root, line), func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					size += info.Size()
				}
			}
			return nil
		})
	}
	if size > copyBudget {
		return nil, nil, refuse("include_too_big", "%s names %d MB to copy into every worktree, and %d MB is the most. Link the large folders instead: share = [...] under [worktrees] in whaleshark.toml.",
			includeFile, size>>20, copyBudget>>20)
	}
	for _, path := range p.Worktrees.Share {
		ok, err := keep("[worktrees] share", path, true)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			links = append(links, path)
		}
	}
	return copies, links, nil
}

// carry copies and links into a new worktree. The place of each is checked
// in the worktree too: a link that git itself put there may lead out of it.
func (t *trees) carry(root, dir string, copies, links []string) error {
	for i, path := range append(copies, links...) {
		if err := cli.Valid("path", path, dir); err != nil {
			fmt.Fprintf(t.warn, "%v in the worktree, and is left out.\n", err)
			continue
		}
		from, to := filepath.Join(root, path), filepath.Join(dir, path)
		err := os.MkdirAll(filepath.Dir(to), 0o700)
		switch _, has := os.Lstat(to); {
		case err != nil:
		case i < len(copies):
			err = copyTree(from, to)
		case has != nil:
			if err = os.Symlink(from, to); err != nil { // a system that lets this login make no link
				fmt.Fprintf(t.warn, "%s could not be linked into the worktree: %v\n", cli.Plain(path), err)
				err = nil
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies a file, or a folder with the files and folders in it.
// Links and everything else that is neither are left out.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return os.MkdirAll(dst, 0o700)
		case !d.Type().IsRegular():
			return nil
		}
		// #nosec G304 G302 -- a path inside the project, checked, written with the mode it has there
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err = io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// leftShown is how many names the notice of what is left behind shows.
const leftShown = 3

func (t *trees) Left(root string) (n int, some []string, err error) {
	out, err := git(root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return 0, nil, nil // no git, no worktrees
	}
	p, err := contract.ReadProjectFile(root)
	if err != nil {
		return 0, nil, err
	}
	quiet := trees{t.k, io.Discard} // Place says what was left out, once
	copies, links, _ := quiet.carried(root, p)
	ours := append(append(copies, links...), contract.ProjectDir, p.Worktrees.Dir)
	for name := range strings.SplitSeq(out, "\x00") {
		name = strings.TrimSuffix(name, "/")
		had := name == ""
		for _, path := range ours {
			path = strings.TrimSuffix(filepath.ToSlash(path), "/")
			had = had || name == path || strings.HasPrefix(name, path+"/")
		}
		if had {
			continue
		}
		if n++; n <= leftShown {
			some = append(some, name)
		}
	}
	return n, some, nil
}
