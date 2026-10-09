package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
)

func under(pkg, parent string) bool { return pkg == parent || strings.HasPrefix(pkg, parent+"/") }

func (g *gate) imports() error {
	out, err := g.cmd(nil, "go", "list", "-m")
	if err != nil {
		return err
	}
	mod := strings.TrimSpace(out) + "/"
	out, err = g.cmd(nil, "go", "list", "-deps", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./...")
	if err != nil {
		return err
	}
	graph := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			graph[f[0]] = f[1:]
		}
	}
	var errs []error
	for _, row := range g.lim["noimport"] {
		// via is, for every package the folder reaches, the one it was reached through.
		via, queue := map[string]string{}, []string{}
		for pkg := range graph {
			if under(pkg, mod+row[0]) {
				via[pkg] = ""
				queue = append(queue, pkg)
			}
		}
		slices.Sort(queue)
		for len(queue) > 0 {
			pkg := queue[0]
			queue = queue[1:]
			for _, banned := range row[1:] {
				if under(pkg, mod+banned) {
					chain := pkg
					for p := via[pkg]; p != ""; p = via[p] {
						chain = p + " -> " + chain
					}
					errs = append(errs, fmt.Errorf("%s may not import %s: %s", row[0], banned, strings.ReplaceAll(chain, mod, "")))
				}
			}
			if strings.HasPrefix(pkg, mod) && pkg != mod+"internal/contract" {
				errs = append(errs, g.storeNamed(row[0], strings.TrimPrefix(pkg, mod))...)
			}
			for _, next := range graph[pkg] {
				if _, seen := via[next]; !seen {
					via[next] = pkg
					queue = append(queue, next)
				}
			}
		}
	}
	for _, row := range g.lim["only"] {
		for _, dir := range []string{"internal", "cmd"} {
			err := g.shipped(dir, func(path string, data []byte) {
				rel, _ := filepath.Rel(g.root, path)
				rel = filepath.ToSlash(rel)
				if filepath.Ext(path) != ".go" || slices.ContainsFunc(row[1:], func(ok string) bool { return under(rel, ok) }) {
					return
				}
				for i, line := range bytes.Split(data, []byte("\n")) {
					if bytes.Contains(line, []byte(row[0])) {
						errs = append(errs, fmt.Errorf("%s:%d: %s belongs in %s only", rel, i+1, row[0], strings.Join(row[1:], ", ")))
					}
				}
			})
			if err != nil {
				return err
			}
		}
	}
	return errors.Join(errs...)
}

// storeNamed finds, in one folder, each place that names a value's Store
// field: the write path taken through the kit, which no import shows. What
// the panes, the page and the connection reach reads through the kit's
// Reader and nothing else. A method called Store, as a counter has, is not
// the field.
func (g *gate) storeNamed(from, dir string) []error {
	files, _ := filepath.Glob(filepath.Join(g.root, dir, "*.go"))
	fset := token.NewFileSet()
	var errs []error
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		called := map[ast.Expr]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, _ := n.(*ast.CallExpr); call != nil {
				called[call.Fun] = true
			}
			if field, _ := n.(*ast.SelectorExpr); field != nil && field.Sel.Name == "Store" && !called[field] {
				errs = append(errs, fmt.Errorf("%s/%s:%d: %s reaches the kit's Store; read through Reader() and start a child command for a change",
					dir, filepath.Base(name), fset.Position(field.Pos()).Line, from))
			}
			return true
		})
	}
	return errs
}
