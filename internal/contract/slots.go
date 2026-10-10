package contract

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// Ports is the login's own block of ports: Count of them from Base.
type Ports struct{ Base, Count int }

// DefaultPorts is the block on a person's own machine; a server gives each
// login its own.
var DefaultPorts = Ports{Base: 20000, Count: 1000}

// Slot is the ports of one task: Size of them, the Nth such group of the
// login's block. It is the task's whether or not the task has a worktree,
// and it is recorded here and nowhere else; a worktree's record only
// repeats N.
type Slot struct {
	N    int    `json:"n"`
	Size int    `json:"size"`
	Root string `json:"root"`
	Run  string `json:"run"`
	Task string `json:"task"`
}

// First is the slot's first port in a block.
func (s Slot) First(block Ports) int { return block.Base + s.N*s.Size }

// Env is what a worker's tab gets for its slot.
func (s Slot) Env(block Ports) []string {
	port := strconv.Itoa(s.First(block))
	return []string{"PORT_BASE=" + port, "PORT=" + port}
}

// Slots is slots.json in the login's state folder: the slots in use, over
// every project of the login.
type Slots struct {
	Versioned
	Taken []Slot `json:"taken"`
}

const slotsFile = "slots.json"

// ErrNoSlot: every port of the login's block is some task's.
var ErrNoSlot = errors.New("no port slot is free in this login's block")

// ReadSlots returns the slots in use.
func ReadSlots(read func(path string) ([]byte, error), stateDir string) ([]Slot, error) {
	var s Slots
	err := ReadVersioned(read, filepath.Join(stateDir, slotsFile), FileVersion, &s)
	return s.Taken, err
}

// changeSlots is the one writer of slots.json: under a lock of its own it
// reads the list, lets fn change it and writes it back.
func changeSlots(p Platform, fn func(taken []Slot) ([]Slot, error)) error {
	dirs, err := p.Dirs()
	if err == nil {
		err = os.MkdirAll(dirs.State, 0o700) // a login's first task finds no folder yet
	}
	if err != nil {
		return err
	}
	unlock, err := p.Lock(filepath.Join(dirs.State, "slots.lock"), true)
	if err != nil {
		return err
	}
	defer unlock()
	taken, err := ReadSlots(p.Read, dirs.State)
	if err == nil {
		taken, err = fn(taken)
	}
	if err != nil {
		return err
	}
	return WriteVersioned(p, filepath.Join(dirs.State, slotsFile), FileVersion, Slots{Versioned{FileVersion}, taken})
}

// TakeSlot gives a task its slot of size ports: the one it has, or the
// lowest whose ports are nobody's. A size of 0 is a project that wants none.
func TakeSlot(p Platform, block Ports, root, run, task string, size int) (slot Slot, err error) {
	if size <= 0 {
		return Slot{}, nil
	}
	root = p.PathKey(root)
	err = changeSlots(p, func(taken []Slot) ([]Slot, error) {
		for _, s := range taken {
			if s.Root == root && s.Run == run && s.Task == task {
				slot = s
				return taken, nil
			}
		}
		for n := 0; (n+1)*size <= block.Count; n++ {
			free := true
			for _, s := range taken {
				free = free && (n*size >= (s.N+1)*s.Size || s.N*s.Size >= (n+1)*size)
			}
			if free {
				slot = Slot{N: n, Size: size, Root: root, Run: run, Task: task}
				return append(taken, slot), nil
			}
		}
		return nil, ErrNoSlot
	})
	return slot, err
}

// FreeSlots gives back every slot gone says is no longer needed, and
// returns them. root is as TakeSlot recorded it: the platform's PathKey.
func FreeSlots(p Platform, gone func(Slot) bool) (freed []Slot, err error) {
	err = changeSlots(p, func(taken []Slot) ([]Slot, error) {
		kept := taken[:0]
		for _, s := range taken {
			if gone(s) {
				freed = append(freed, s)
			} else {
				kept = append(kept, s)
			}
		}
		return kept, nil
	})
	return freed, err
}
