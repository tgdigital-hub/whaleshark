// Package window is the window, `open`: a person's terminal joined to the
// keeper. It puts the terminal in raw mode, says its size, copies bytes both
// ways and puts the terminal back. It draws nothing and decides nothing.
package window

import "github.com/tgdigital-hub/whaleshark/internal/contract"

// Plug binds `open`.
func Plug(k *contract.Kit) { k.Handle("open", open) }
