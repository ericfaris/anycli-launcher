//go:build windows

package worktree

import (
	"errors"

	"golang.org/x/sys/windows"
)

const stillActive = 259 // STILL_ACTIVE

// processAlive opens the process and checks it hasn't exited. Access
// denied means it exists but belongs to someone else.
func processAlive(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true, nil
		}
		return false, nil
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false, err
	}
	return code == stillActive, nil
}

func processCwd(int) (string, error) { return "", errUnsupported }
