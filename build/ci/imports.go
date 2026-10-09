package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// storeWrites is every method the store has beyond reading.
func storeWrites() map[string]bool {
	store, reader := reflect.TypeFor[contract.Store](), reflect.TypeFor[contract.Reader]()
	w := map[string]bool{}
	for i := range store.NumMethod() {
		if _, reads := reader.MethodByName(store.Method(i).Name); !reads {
			w[store.Method(i).Name] = true
		}
	}
	return w
}

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
				errs = append(errs, g.writeCalls(row[0], strings.TrimPrefix(pkg, mod))...)
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

// writeCalls finds, in one folder, each call of a writing method on a
// value's Store field: the write path taken through the kit, which no
// import shows.
func (g *gate) writeCalls(from, dir string) []error {
	files, _ := filepath.Glob(filepath.Join(g.root, dir, "*.go"))
	fset, writes := token.NewFileSet(), storeWrites()
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
		ast.Inspect(file, func(n ast.Node) bool {
			call, _ := n.(*ast.SelectorExpr)
			if call == nil || !writes[call.Sel.Name] {
				return true
			}
			if field, _ := call.X.(*ast.SelectorExpr); field != nil && field.Sel.Name == "Store" {
				errs = append(errs, fmt.Errorf("%s/%s:%d: %s reaches a call of Store.%s; start a child command instead",
					dir, filepath.Base(name), fset.Position(call.Pos()).Line, from, call.Sel.Name))
			}
			return true
		})
	}
	return errs
}
