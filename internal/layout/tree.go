package layout

import (
	"errors"
	"slices"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The smallest pane, in cells. A split that would leave less is refused, and
// no resize or drag goes under it.
const (
	LeastW = 8
	LeastH = 2
)

// What a call is refused with, beside contract.ErrNoPane.
var (
	ErrNoRoom    = errors.New("no room for another pane there")
	ErrNoLine    = errors.New("no dividing line to move there")
	ErrDirection = errors.New("no such direction")
	ErrOtherTab  = errors.New("the two panes are in different tabs")
)

// Node is a pane, or a split: two nodes with a dividing line of one cell
// between them, A left of B, or above it when Down is set. Share is A's part
// of the cells the two have together. Cells above 0 holds one of them at that
// many cells whatever the window does: B when Last is set, else A.
type Node struct {
	Pane  string  `json:"pane,omitempty"`
	Down  bool    `json:"down,omitempty"`
	Share float64 `json:"share,omitempty"`
	Cells int     `json:"cells,omitempty"`
	Last  bool    `json:"last,omitempty"`
	A     *Node   `json:"a,omitempty"`
	B     *Node   `json:"b,omitempty"`
}

// Tab is one tab: its tree, the pane that has the keys in it, and the pane
// zoomed to the whole tab, if one is.
type Tab struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Focus string `json:"focus"`
	Zoom  string `json:"zoom,omitempty"`
	Root  *Node  `json:"root"`
}

// Layout is every tab, in the order of the tab row, at one window size. The
// first row of the window is the tab row, the second a line under it, and the
// rest is the tab that shows. A layout with no size yet refuses no split.
type Layout struct {
	Tabs   []*Tab `json:"tabs"`
	Active int    `json:"active"`
	W, H   int    `json:"-"`
	// Over is a pane drawn over the tab that shows, and OverAt its inside:
	// the keeper's, kept here so that At can say a click landed in it.
	Over   string `json:"-"`
	OverAt Rect   `json:"-"`
	first  int    // the first tab the tab row shows
}

// leaf finds a pane's node under n and the split it is in, up for n itself.
func (n *Node) leaf(pane string, up *Node) (leaf, parent *Node) {
	if n.A == nil {
		if n.Pane == pane {
			return n, up
		}
		return nil, nil
	}
	if leaf, parent = n.A.leaf(pane, n); leaf != nil {
		return leaf, parent
	}
	return n.B.leaf(pane, n)
}

// Panes lists a tab's panes, left to right and top to bottom.
func (t *Tab) Panes() []string {
	var out []string
	walk(t.Root, Rect{}, func(n *Node, _ Rect) {
		if n.A == nil {
			out = append(out, n.Pane)
		}
	})
	return out
}

// Tab is the tab with that id.
func (l *Layout) Tab(id string) *Tab {
	if i := slices.IndexFunc(l.Tabs, func(t *Tab) bool { return t.ID == id }); i >= 0 {
		return l.Tabs[i]
	}
	return nil
}

// Of is the tab a pane is in.
func (l *Layout) Of(pane string) *Tab {
	for _, t := range l.Tabs {
		if n, _ := t.Root.leaf(pane, nil); n != nil {
			return t
		}
	}
	return nil
}

// Add opens a tab of one pane after the others, without showing it.
func (l *Layout) Add(id, label, pane string) *Tab {
	t := &Tab{ID: id, Label: label, Focus: pane, Root: &Node{Pane: pane}}
	l.Tabs = append(l.Tabs, t)
	return t
}

// Remove closes a tab. The tab after it shows in its place.
func (l *Layout) Remove(id string) {
	i := slices.IndexFunc(l.Tabs, func(t *Tab) bool { return t.ID == id })
	if i < 0 {
		return
	}
	l.Tabs = slices.Delete(l.Tabs, i, i+1)
	if l.Active > i || l.Active == len(l.Tabs) {
		l.Active = max(l.Active-1, 0)
	}
	l.reveal()
}

// Show makes a tab the one that shows.
func (l *Layout) Show(id string) bool {
	i := slices.IndexFunc(l.Tabs, func(t *Tab) bool { return t.ID == id })
	if i >= 0 {
		l.Active = i
		l.reveal()
	}
	return i >= 0
}

// Fit takes the window's size in cells. A new size brings the tab that
// shows back onto the tab row.
func (l *Layout) Fit(w, h int) {
	if w, h = max(w, 0), max(h, 0); w != l.W || h != l.H {
		l.W, l.H = w, h
		l.reveal()
	}
}

// Split opens the pane fresh, an id no pane has, beside pane. A ratio of at
// most 1 is the share pane keeps, and none is a half; above 1 it is fresh's
// size in cells, columns to the side and rows above or below, kept when the
// window changes.
func (l *Layout) Split(pane, direction string, ratio float64, fresh string) error {
	t := l.Of(pane)
	if t == nil {
		return contract.ErrNoPane
	}
	s := Node{A: &Node{Pane: pane}, B: &Node{Pane: fresh}, Share: 0.5, Last: true}
	switch direction {
	case contract.Right:
	case contract.Left:
		s.A, s.B, s.Last = s.B, s.A, false
	case contract.Down:
		s.Down = true
	case contract.Up:
		s.A, s.B, s.Last, s.Down = s.B, s.A, false, true
	default:
		return ErrDirection
	}
	if ratio > 1 {
		s.Cells = int(min(ratio, 1<<15))
	} else if ratio > 0 {
		s.Share = ratio
	}
	if !s.Last {
		s.Share = 1 - s.Share
	}
	n, _ := t.Root.leaf(pane, nil)
	if r := l.place(t, n); l.W > 0 && l.H > 0 && (s.Down && r.H < 2*LeastH+1 || !s.Down && r.W < 2*LeastW+1) {
		return ErrNoRoom
	}
	*n, t.Zoom = s, ""
	return nil
}

// Close takes a pane out, and its neighbour takes its place. A tab goes with
// its last pane, which gone reports.
func (l *Layout) Close(pane string) (gone bool) {
	t := l.Of(pane)
	if t == nil {
		return false
	}
	n, up := t.Root.leaf(pane, nil)
	if up == nil {
		l.Remove(t.ID)
		return true
	}
	if up.A == n {
		*up = *up.B
	} else {
		*up = *up.A
	}
	if t.Zoom = ""; t.Focus == pane {
		for ; up.A != nil; up = up.A {
		}
		t.Focus = up.Pane
	}
	return false
}

// Swap makes two panes of one tab change places.
func (l *Layout) Swap(a, b string) error {
	t := l.Of(a)
	if t == nil || l.Of(b) == nil {
		return contract.ErrNoPane
	} else if l.Of(b) != t {
		return ErrOtherTab
	}
	na, _ := t.Root.leaf(a, nil)
	nb, _ := t.Root.leaf(b, nil)
	na.Pane, nb.Pane, t.Zoom = b, a, ""
	return nil
}

// Focus gives a pane the keys of its tab. Another pane's zoom ends.
func (l *Layout) Focus(pane string) bool {
	t := l.Of(pane)
	if t == nil {
		return false
	}
	if t.Focus = pane; t.Zoom != pane {
		t.Zoom = ""
	}
	return true
}

// Zoom gives a pane its whole tab and the keys, or, zoomed already, puts it back.
func (l *Layout) Zoom(pane string) bool {
	t := l.Of(pane)
	if t == nil {
		return false
	}
	if t.Zoom == pane {
		t.Zoom = ""
	} else {
		t.Zoom, t.Focus = pane, pane
	}
	return true
}

// Sound reports whether a layout read from a file can be used: every split
// has two sides and a share, every pane and tab an id nothing else has, and
// what a tab names as focused or zoomed is a pane of its own.
func (l *Layout) Sound() bool {
	seen := map[string]bool{}
	fresh := func(kind, id string) bool {
		ok := id != "" && !seen[kind+id]
		seen[kind+id] = true
		return ok
	}
	var sound func(n *Node) bool
	sound = func(n *Node) bool {
		if n == nil {
			return false
		} else if n.A == nil || n.B == nil {
			return n.A == nil && n.B == nil && fresh("pane ", n.Pane)
		}
		return n.Pane == "" && n.Share >= 0 && n.Share <= 1 && n.Cells >= 0 && sound(n.A) && sound(n.B)
	}
	for _, t := range l.Tabs {
		if t == nil || !fresh("tab ", t.ID) || !sound(t.Root) ||
			!slices.Contains(t.Panes(), t.Focus) || t.Zoom != "" && !slices.Contains(t.Panes(), t.Zoom) {
			return false
		}
	}
	return l.Active >= 0 && l.Active < max(len(l.Tabs), 1)
}
