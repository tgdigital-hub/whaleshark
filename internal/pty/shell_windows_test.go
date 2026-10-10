package pty

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// What the test of a thousand shells needs of this system.
var (
	shell   = []string{"cmd.exe"}
	sayMark = "echo got-%WS_MARK%"
	linger  = "start /b ping -n 600 localhost >nul"
)

// watch holds the terminal's program and the programs it has started, and
// gives a function that lists which of them are still running ten seconds
// on. A process id alone would not do: Windows gives one away at once.
func watch(t *testing.T, p contract.Pty) func() []int {
	leader, err := windows.GetProcessId(windows.Handle(p.(*console).proc))
	held := map[uint32]windows.Handle{}
	var all windows.Handle
	if err == nil {
		all, err = windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	}
	if err != nil {
		t.Error(err) // not Fatal: this runs beside the test's own goroutine
		return func() []int { return nil }
	}
	defer windows.CloseHandle(all)
	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(all, &entry); err == nil; err = windows.Process32Next(all, &entry) {
		if entry.ProcessID != leader && entry.ParentProcessID != leader {
			continue
		}
		if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, entry.ProcessID); err == nil {
			held[entry.ProcessID] = h
		}
	}
	return func() (left []int) {
		for pid, h := range held {
			if event, _ := windows.WaitForSingleObject(h, 10_000); event != windows.WAIT_OBJECT_0 {
				left = append(left, int(pid))
			}
			windows.CloseHandle(h)
		}
		return left
	}
}
