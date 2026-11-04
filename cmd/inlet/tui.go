package main

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// rawInput asks stty for unbuffered, unechoed input so single keys arrive
// immediately. It returns a restore func that is a no-op when stdin is not a
// terminal. ISIG is left alone so Ctrl-C still quits.
func rawInput() func() {
	saved, err := stty("-g")
	if err != nil {
		return func() {}
	}
	if _, err := stty(saved, "-icanon", "-echo"); err != nil {
		return func() {}
	}
	return func() { stty(saved) }
}

func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// readInput dispatches single keypresses until r closes.
func readInput(r *bufio.Reader, onKey func(byte)) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		onKey(b)
	}
}

func copyClipboard(s string) error {
	var tools [][]string
	switch runtime.GOOS {
	case "darwin":
		tools = [][]string{{"pbcopy"}}
	default:
		tools = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "-ib"}}
	}
	for _, t := range tools {
		cmd := exec.Command(t[0], t[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return exec.ErrNotFound
}
