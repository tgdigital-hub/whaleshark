package pty

import (
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Start makes a pseudo-console and starts the program attached to it.
func Start(s contract.PtySpec) (contract.Pty, error) { return startConsole(system{}, s) }

// system is Windows itself.
type system struct{}

// endWithLastHandle is the one limit of a pane's job: when the keeper's
// handle of it goes, by Close or with the keeper itself, every process in
// it ends. The system is given its address as a number, so it is a variable
// of the package, which never moves.
var endWithLastHandle = windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
	LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE}}

func coord(cols, rows int) windows.Coord {
	return windows.Coord{X: int16(cols), Y: int16(rows)} // #nosec G115 -- sized holds both under maxSide
}

// Console makes the two pipes and the pseudo-console between them. The
// pseudo-console keeps its ends of the pipes for itself, so ours are closed.
func (system) Console(cols, rows int) (uintptr, io.WriteCloser, io.ReadCloser, error) {
	var inRead, inWrite, outRead, outWrite, pc windows.Handle
	err := windows.CreatePipe(&inRead, &inWrite, nil, 0)
	if err == nil {
		if err = windows.CreatePipe(&outRead, &outWrite, nil, 0); err == nil {
			err = windows.CreatePseudoConsole(coord(cols, rows), inRead, outWrite, 0, &pc)
		}
	}
	system{}.Free(uintptr(inRead), uintptr(outWrite))
	if err != nil {
		system{}.Free(uintptr(inWrite), uintptr(outRead))
		return 0, nil, nil, err
	}
	return uintptr(pc), os.NewFile(uintptr(inWrite), "typed"), os.NewFile(uintptr(outRead), "printed"), nil
}

func (system) Resize(pc uintptr, cols, rows int) error {
	return windows.ResizePseudoConsole(windows.Handle(pc), coord(cols, rows))
}

func (system) Shut(pc uintptr) { windows.ClosePseudoConsole(windows.Handle(pc)) }

// Start starts the program stopped, puts it into a job that ends with its
// last handle, and only then lets it run, so that nothing it starts is
// outside the job.
func (system) Start(pc uintptr, path, line, dir string, env []uint16) (proc, job uintptr, err error) {
	var words [3]*uint16
	for i, s := range []string{path, line, dir} {
		if s == "" {
			continue // with no folder named, the program starts in this process's
		}
		if words[i], err = windows.UTF16PtrFromString(s); err != nil {
			return 0, 0, err
		}
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, 0, err
	}
	defer attrs.Delete()
	// The attribute's value is the pseudo-console's handle itself, which is
	// a pointer of the system's and none of ours.
	handle, size := *(*unsafe.Pointer)(unsafe.Pointer(&pc)), unsafe.Sizeof(pc) // #nosec G103 -- read as the pointer it is
	err = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, handle, size)
	if err != nil {
		return 0, 0, err
	}
	held, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, 0, err
	}
	// #nosec G103 -- the system reads the limits from here
	_, err = windows.SetInformationJobObject(held, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&endWithLastHandle)), uint32(unsafe.Sizeof(endWithLastHandle)))
	start := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	start.Cb = uint32(unsafe.Sizeof(start)) // #nosec G103 -- the size the system asks for
	var made windows.ProcessInformation
	if err == nil {
		err = windows.CreateProcess(words[0], words[1], nil, nil, false,
			windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_SUSPENDED,
			&env[0], words[2], &start.StartupInfo, &made)
	}
	if err != nil {
		system{}.Free(uintptr(held))
		return 0, 0, err
	}
	defer system{}.Free(uintptr(made.Thread))
	if err = windows.AssignProcessToJobObject(held, made.Process); err == nil {
		_, err = windows.ResumeThread(made.Thread)
	}
	if err != nil {
		_ = windows.TerminateProcess(made.Process, 1)
		system{}.Free(uintptr(made.Process), uintptr(held))
		return 0, 0, err
	}
	return uintptr(made.Process), uintptr(held), nil
}

func (system) Wait(proc uintptr) (int, error) {
	if _, err := windows.WaitForSingleObject(windows.Handle(proc), windows.INFINITE); err != nil {
		return -1, err
	}
	var code uint32
	err := windows.GetExitCodeProcess(windows.Handle(proc), &code)
	return int(code), err
}

func (system) End(job uintptr) { _ = windows.TerminateJobObject(windows.Handle(job), 1) }

func (system) Free(handles ...uintptr) {
	for _, h := range handles {
		_ = windows.CloseHandle(windows.Handle(h))
	}
}
