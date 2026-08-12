//go:build windows

package claude

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessTree struct {
	mutex  sync.Mutex
	handle windows.Handle
	closed bool
}

func newProcessTree(command *exec.Cmd) (*windowsProcessTree, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
	return &windowsProcessTree{handle: handle}, nil
}

func (tree *windowsProcessTree) Attach(process *os.Process) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.AssignProcessToJobObject(tree.handle, handle)
}

func (tree *windowsProcessTree) Terminate() error {
	tree.mutex.Lock()
	defer tree.mutex.Unlock()
	if tree.closed {
		return os.ErrProcessDone
	}
	return windows.TerminateJobObject(tree.handle, 1)
}

func (tree *windowsProcessTree) Close() error {
	tree.mutex.Lock()
	defer tree.mutex.Unlock()
	if tree.closed {
		return nil
	}
	tree.closed = true
	return windows.CloseHandle(tree.handle)
}

func platformEnvironmentNames() []string {
	return []string{
		"PATH",
		"PATHEXT",
		"SYSTEMROOT",
		"WINDIR",
		"COMSPEC",
		"TEMP",
		"TMP",
		"USERPROFILE",
		"APPDATA",
		"LOCALAPPDATA",
	}
}
