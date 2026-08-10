//go:build !windows

package codex

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const processTerminationGrace = 2 * time.Second

type unixProcessTree struct {
	mutex sync.Mutex
	pid   int
}

func newProcessTree(command *exec.Cmd) (*unixProcessTree, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &unixProcessTree{}, nil
}

func (tree *unixProcessTree) Attach(process *os.Process) error {
	tree.mutex.Lock()
	defer tree.mutex.Unlock()
	tree.pid = process.Pid
	return nil
}

func (tree *unixProcessTree) Terminate() error {
	tree.mutex.Lock()
	pid := tree.pid
	tree.mutex.Unlock()
	if pid == 0 {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	time.Sleep(processTerminationGrace)
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (*unixProcessTree) Close() error {
	return nil
}

func platformEnvironmentNames() []string {
	return []string{"PATH", "HOME", "TMPDIR", "USER", "LOGNAME", "SHELL", "CODEX_HOME"}
}
