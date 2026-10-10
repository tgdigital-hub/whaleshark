package keeper

import "golang.org/x/sys/unix"

// peer asks the system which login is on the other end of a socket.
func peer(fd uintptr) (int, error) {
	x, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) // #nosec G115 -- a file's number
	if err != nil {
		return 0, err
	}
	return int(x.Uid), nil
}
