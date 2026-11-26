package shim

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestScripts(t *testing.T) {
	if !reflect.DeepEqual(Shells(), []string{"bash", "fish", "pwsh", "zsh"}) {
		t.Fatal(Shells())
	}
	for _, sh := range Shells() {
		s, err := Script(sh)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, "--cd-file") || !strings.Contains(s, "ac init "+sh) {
			t.Errorf("%s script incomplete:\n%s", sh, s)
		}
	}
	if p, _ := Script("powershell"); !strings.Contains(p, "Remove-Item -Path Alias:ac") {
		t.Fatal("pwsh must drop the built-in ac alias")
	}
	if _, err := Script("tcsh"); err == nil {
		t.Fatal("want error")
	}
}

// Syntax-check each script with its own shell when installed.
func TestScriptsParse(t *testing.T) {
	checks := map[string][]string{"bash": {"bash", "-n"}, "zsh": {"zsh", "-n"}, "fish": {"fish", "--no-execute"}}
	for sh, argv := range checks {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		s, _ := Script(sh)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", sh, err, out)
		}
	}
}
