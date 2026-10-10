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

// born is when a process was started, in the system's own count.
func born(h windows.Handle) (int64, error) {
	var made, ended, kernel, user windows.Filetime
	err := windows.GetProcessTimes(h, &made, &ended, &kernel, &user)
	return made.Nanoseconds(), err
}

// watch holds the terminal's program and the programs it has started, and
// gives a function that lists which of them are still running ten seconds
// on. A process id alone would not do: Windows gives one away at once. For
// the same reason a process that names the terminal's program as its parent
// is its child only if it was started after it: a parent's id is never
// cleared, so a program whose parent ended long ago looks like the child of
// whichever shell of the thousand is next given that number.
func watch(t *testing.T, p contract.Pty) func() []int {
	proc := windows.Handle(p.(*console).proc)
	leader, err := windows.GetProcessId(proc)
	var since int64
	if err == nil {
		since, err = born(proc)
	}
	held, names := map[uint32]windows.Handle{}, map[uint32]string{}
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
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if err != nil {
			continue
		}
		if at, err := born(h); entry.ProcessID != leader && err == nil && at < since {
			windows.CloseHandle(h) // older than the shell: another program's child
			continue
		}
		held[entry.ProcessID], names[entry.ProcessID] = h, windows.UTF16ToString(entry.ExeFile[:])
	}
	return func() (left []int) {
		for pid, h := range held {
			if event, _ := windows.WaitForSingleObject(h, 10_000); event != windows.WAIT_OBJECT_0 {
				left = append(left, int(pid))
				t.Logf("process %d of shell %d is %s", pid, leader, names[pid])
			}
			windows.CloseHandle(h)
		}
		return left
	}
}
