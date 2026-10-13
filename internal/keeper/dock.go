package keeper

import (
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/layout"
	"github.com/tgdigital-hub/whaleshark/internal/screen"
)

// ours is a pane program of ours: the keeper's own file and three fixed words.
func (k *Keeper) ours(name string) []string {
	self, _ := k.kit.Platform.SelfPath()
	return []string{self, "ui", "run", name}
}

// none is the dock's terminal before its first start, and all asked of it.
type none struct{ contract.Pty }

func (none) Write([]byte) (int, error) { return 0, contract.ErrNoPane }
func (none) Close() error              { return nil }
func (none) Resize(int, int) error     { return nil }

// dock makes the dock, the one pane that is in no tab and shows beside every
// tab: anew at every start of the keeper and from no file, at the edge and
// the width of the person's settings. What its program shows is not known here.
func (k *Keeper) dock() {
	k.mu.Lock()
	defer k.mu.Unlock()
	p := &pane{rec: contract.Pane{ID: layout.DockPane, Label: contract.SideTitle, Status: contract.StatusUnknown, Dock: true},
		tty: none{}, scr: screen.New(firstW, firstH), w: firstW, h: firstH}
	k.panes[p.rec.ID] = p
	k.lay.Dock = layout.Dock{On: true, Right: k.set.Side.Place == contract.Right, Width: k.set.Side.Width}
	k.changed()
	k.side(p)
}

// side starts the dock's program and tells the readers of events of its new
// terminal. A start that fails is an end like another.
func (k *Keeper) side(p *pane) {
	if p.argv = k.ours("side"); k.start(p) == nil {
		k.emit(contract.EvState, p.rec)
	} else {
		k.ended(p)
	}
}

var dockEnds, dockWait = 10 * time.Second, 5 * time.Second

// ended is the end of the dock's program, whose place is never lost: the
// keeper draws it until a program has printed there, and starts the program
// again at once, or after three ends within dockEnds every dockWait.
func (k *Keeper) ended(p *pane) {
	now, wait := time.Now(), time.Duration(0)
	p.count.Store(0)
	if k.placed != "" {
		k.away() // its menu has nobody to answer to
		k.changed()
	}
	if k.ends = append(k.ends, now)[max(len(k.ends)-2, 0):]; len(k.ends) == 3 && now.Sub(k.ends[0]) < dockEnds {
		wait = dockWait
	}
	time.AfterFunc(wait, func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.panes[p.rec.ID] == p { // gone once the keeper has stopped
			k.side(p)
		}
	})
	k.wake()
}
