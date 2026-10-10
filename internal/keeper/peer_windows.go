package keeper

import "os"

// peer cannot ask on Windows: the system has no such question for a socket
// file, and the folder's permissions are the whole defence there.
func peer(uintptr) (int, error) { return os.Getuid(), nil }
