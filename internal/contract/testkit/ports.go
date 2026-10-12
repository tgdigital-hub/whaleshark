package testkit

import (
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Block claims one login's block of ports in the server's range for the
// test that asks, and returns its first port; the claim lasts until release
// is called or the test's program ends. The tests that have the program
// under test listen in a block run side by side, in several packages and in
// several copies of the tree on one machine, and each needs a block nobody
// else is using. The claim is a listener the test itself holds on a port of
// the block that the program never opens: the third from its end, where
// the last is the door and the one before it is where the server's proof
// listens. So a block another test has is passed by, from the moment it
// was chosen. When every block is taken, Block waits for one.
func Block() (base int, release func(), err error) {
	listen := func(port int) (net.Listener, error) {
		return net.Listen("tcp4", net.JoinHostPort(contract.Loopback, strconv.Itoa(port)))
	}
	for end := time.Now().Add(2 * time.Minute); ; time.Sleep(100 * time.Millisecond) {
		for base = contract.ServerPorts.Base + contract.ServerPorts.Count - contract.BlockSize; base >= contract.ServerPorts.Base; base -= contract.BlockSize {
			held, err := listen(base + contract.BlockSize - 3)
			if err != nil {
				continue
			}
			free := true
			for _, port := range []int{base + contract.BlockSize - 2, base + contract.BlockSize - 1} {
				l, err := listen(port)
				if free = free && err == nil; err == nil {
					l.Close()
				}
			}
			if free {
				return base, func() { held.Close() }, nil
			}
			held.Close()
		}
		if time.Now().After(end) {
			return 0, nil, errors.New("no block of ports was free on this machine for two minutes")
		}
	}
}
