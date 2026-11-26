package app

import (
	"bufio"
	"strings"
)

// ask prints prompt and reads one line. ok is false on EOF with no input,
// which every flow treats as "cancel".
func (a *app) ask(prompt string) (string, bool) {
	if a.in == nil {
		a.in = bufio.NewReader(a.env.Stdin)
	}
	a.printf("%s", prompt)
	line, err := a.in.ReadString('\n')
	if err != nil && line == "" {
		a.printf("\n")
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}

// yes asks a yes/no question; def is the answer for a blank line. EOF is
// always "no".
func (a *app) yes(prompt string, def bool) bool {
	ans, ok := a.ask(prompt)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}
