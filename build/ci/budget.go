package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// shipped walks the files of a folder that count as shipped and hands each
// to fn. A folder that does not exist yet has none.
func (g *gate) shipped(dir string, fn func(path string, data []byte)) error {
	exts, skip := g.lim.words("count"), g.lim.words("skip")
	err := filepath.WalkDir(filepath.Join(g.root, dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		skipped := slices.ContainsFunc(skip, func(s string) bool { return strings.HasSuffix(d.Name(), s) })
		if d.IsDir() {
			if skipped {
				return filepath.SkipDir
			}
			return nil
		}
		if skipped || !slices.Contains(exts, filepath.Ext(path)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			fn(path, data)
		}
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (g *gate) lines() error {
	var errs []error
	total, budgeted := 0, map[string]bool{}
	for _, row := range g.lim["lines"] {
		if len(row) < 3 {
			return fmt.Errorf("a lines line needs a limit, a name and a folder: %v", row)
		}
		limit, err := strconv.Atoi(row[0])
		if err != nil {
			return err
		}
		n := 0
		for _, dir := range row[2:] {
			budgeted[dir] = true
			err := g.shipped(dir, func(_ string, data []byte) {
				n += bytes.Count(data, []byte("\n"))
				if len(data) > 0 && data[len(data)-1] != '\n' {
					n++
				}
			})
			if err != nil {
				return err
			}
		}
		total += n
		note := ""
		if n > limit {
			note = "  over its budget"
		}
		if n*10 > limit*11 {
			note = "  MORE THAN A TENTH OVER"
			errs = append(errs, fmt.Errorf("%s has %d lines, more than a tenth over its budget of %d", row[1], n, limit))
		}
		fmt.Fprintf(g.out, "  %-10s %6d of %6d%s\n", row[1], n, limit, note)
	}
	dirs, _ := os.ReadDir(filepath.Join(g.root, "internal"))
	for _, d := range dirs {
		if d.IsDir() && !budgeted["internal/"+d.Name()] {
			errs = append(errs, fmt.Errorf("internal/%s has no lines line in limits.txt", d.Name()))
		}
	}
	limit, err := g.lim.num("total")
	if err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  %-10s %6d of %6d\n", "total", total, limit)
	if total > limit {
		errs = append(errs, fmt.Errorf("%d lines in all, the cap is %d", total, limit))
	}
	return errors.Join(errs...)
}

func (g *gate) modules() error {
	limit, err := g.lim.num("modules")
	if err != nil {
		return err
	}
	out, err := g.cmd(nil, "go", "list", "-m", "-f", "{{if not .Main}}{{.Path}}{{end}}", "all")
	if err != nil {
		return err
	}
	var errs []error
	mods, named := strings.Fields(out), g.lim.words("module")
	for _, m := range mods {
		if !slices.Contains(named, m) {
			errs = append(errs, fmt.Errorf("module %s is not named in limits.txt", m))
		}
	}
	if len(mods) > limit {
		errs = append(errs, fmt.Errorf("%d modules, at most %d", len(mods), limit))
	}
	fmt.Fprintf(g.out, "  %d of %d modules\n", len(mods), limit)
	return errors.Join(errs...)
}

// build makes the program file as it ships, for one system, and returns
// where it is.
func (g *gate) build(dir, target string) (string, error) {
	goos, goarch, _ := strings.Cut(target, "/")
	file := filepath.Join(dir, "whaleshark-"+goos+"-"+goarch)
	if goos == "windows" {
		file += ".exe"
	}
	_, err := g.cmd([]string{"GOOS=" + goos, "GOARCH=" + goarch},
		"go", "build", "-trimpath", "-ldflags=-s -w", "-o", file, "./cmd/whaleshark")
	return file, err
}

func (g *gate) cross() error {
	limit, err := g.lim.num("size")
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "whaleshark-ci")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var errs []error
	for _, target := range g.lim.words("target") {
		file, err := g.build(dir, target)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  %-14s %9d of %d bytes\n", target, info.Size(), limit)
		if info.Size() > int64(limit) {
			errs = append(errs, fmt.Errorf("the file for %s is %d bytes, at most %d", target, info.Size(), limit))
		}
	}
	return errors.Join(errs...)
}

func (g *gate) page() error {
	row := g.lim.words("page")
	if len(row) != 2 {
		return errors.New("limits.txt needs one page line: a figure and a folder")
	}
	limit, err := strconv.Atoi(row[0])
	if err != nil {
		return err
	}
	size := 0
	err = g.shipped(row[1], func(path string, data []byte) {
		if filepath.Ext(path) != ".go" {
			size += len(data)
		}
	})
	fmt.Fprintf(g.out, "  %d of %d bytes\n", size, limit)
	if err == nil && size > limit {
		err = fmt.Errorf("the page is %d bytes, at most %d", size, limit)
	}
	return err
}

func (g *gate) commands() error {
	limit, err := g.lim.num("commands")
	if err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  %d of %d commands\n", len(contract.Commands), limit)
	if len(contract.Commands) > limit {
		return fmt.Errorf("%d commands, at most %d", len(contract.Commands), limit)
	}
	return nil
}

// timing runs each timed command 21 times and holds the middle run to its
// limit. A command that is not built yet shows only what starting costs.
func (g *gate) timing() error {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(g.out, "  SKIPPED: no time is promised on Windows")
		return nil
	}
	dir, err := os.MkdirTemp("", "whaleshark-ci")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file, err := g.build(dir, runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		return err
	}
	var errs []error
	for _, row := range g.lim["time"] {
		limit, err := time.ParseDuration(row[0])
		if err != nil {
			return err
		}
		took, note := make([]time.Duration, 21), ""
		for i := range took {
			start := time.Now()
			_, err := g.cmd(nil, file, row[1:]...)
			took[i] = time.Since(start)
			if err != nil && strings.Contains(err.Error(), contract.ErrNotBuilt.Error()) {
				note = "  (not built yet: this is the start alone)"
			}
		}
		slices.Sort(took)
		mid := took[len(took)/2].Round(100 * time.Microsecond)
		fmt.Fprintf(g.out, "  %-16s %8v of %v%s\n", strings.Join(row[1:], " "), mid, limit, note)
		if mid > limit {
			errs = append(errs, fmt.Errorf("%s took %v, at most %v", strings.Join(row[1:], " "), mid, limit))
		}
	}
	return errors.Join(errs...)
}
