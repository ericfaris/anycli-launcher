//go:build unix

package launch

import (
	"os"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// execProcess replaces this process with argv (the CLI owns the tab and
// its signals). It only returns on failure. dir is already the cwd.
func execProcess(argv []string, dir string, env []string) (int, error) {
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			return 1, err
		}
	}
	err := syscall.Exec(argv[0], argv, env)
	return 127, err
}

// drainTTY discards pending input: raw, no echo; wait 150ms for the
// handshake replies, then read in 50ms windows until one comes back empty
// (at most 50 rounds). The terminal state is always restored.
func drainTTY(f *os.File) {
	fd := f.Fd()
	if !term.IsTerminal(fd) {
		return
	}
	st, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	defer term.Restore(fd, st)
	time.Sleep(150 * time.Millisecond)
	buf := make([]byte, 4096)
	for i := 0; i < 50; i++ {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 50)
		if err != nil || n == 0 {
			return
		}
		if m, err := unix.Read(int(fd), buf); err != nil || m <= 0 {
			return
		}
	}
}
