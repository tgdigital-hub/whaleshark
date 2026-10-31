//go:build windows

package rehearse

import "errors"

// killProcess is not played on Windows: the rehearsal is for Linux and Mac.
func killProcess(int) error { return errors.New("the rehearsal does not run on Windows") }
