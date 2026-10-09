package view

import (
	"fmt"
	"strings"
)

// diff lists the lines two texts do not share, in order.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < max(len(w), len(g)); i++ {
		var x, y string
		if i < len(w) {
			x = w[i]
		}
		if i < len(g) {
			y = g[i]
		}
		if x != y {
			fmt.Fprintf(&b, "line %d\n  want %q\n  got  %q\n", i+1, x, y)
		}
	}
	return b.String()
}
