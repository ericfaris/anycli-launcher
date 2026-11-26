//go:build unix

package worktree

import (
	"errors"
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// processAlive sends signal 0: no error or EPERM (someone else's process)
// means it exists.
func processAlive(pid int) (bool, error) {
	err := unix.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, unix.EPERM):
		return true, nil
	case errors.Is(err, unix.ESRCH):
		return false, nil
	}
	return false, err
}

// processCwd reads /proc/<pid>/cwd (Linux only).
func processCwd(pid int) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errUnsupported
	}
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
}
