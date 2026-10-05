package cmd

import (
	"os"
	"os/exec"
	"runtime"
)

func stdout() *os.File { return os.Stdout }
func openURL(url string) error {
	program := "xdg-open"
	if runtime.GOOS == "darwin" {
		program = "open"
	}
	return exec.Command(program, url).Run()
}
