// Command ac picks a project, cds into it (via the shell shim) and launches
// an AI CLI there. See `ac help`.
package main

import (
	"errors"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ericfaris/anycli-launcher/internal/app"
	"github.com/ericfaris/anycli-launcher/internal/clis"
	"github.com/ericfaris/anycli-launcher/internal/tui"
)

func main() {
	os.Exit(app.Main(os.Args[1:], realEnv()))
}

func realEnv() *app.Env {
	return &app.Env{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		IsTTY:   isTTY(os.Stdin),
		RunMenu: runMenu,
		Editor:  editor(),
	}
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func runMenu(o tui.Options) (tui.Result, error) {
	m, err := tea.NewProgram(tui.New(o), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout)).Run()
	if err != nil {
		return tui.Result{}, err
	}
	res, ok := m.(tui.Model)
	if !ok {
		return tui.Result{}, errors.New("unexpected menu model")
	}
	return res.Result(), nil
}

// editor returns an opener for $VISUAL / $EDITOR, or nil when neither is set.
func editor() func(string) error {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	argv := clis.SplitArgs(ed)
	if len(argv) == 0 {
		return nil
	}
	return func(path string) error {
		cmd := exec.Command(argv[0], append(argv[1:], path)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
}
