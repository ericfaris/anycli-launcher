//go:build !unix

package launch

import (
	"os"
	"os/exec"
	"os/signal"
)

// execProcess runs argv as a child (Windows has no exec) and waits.
func execProcess(argv []string, dir string, env []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	return exitCode(cmd.Run())
}

// drainTTY is a no-op: the stray-reply problem was seen on WSL/Unix ptys.
func drainTTY(*os.File) {}
