// Build tooling remains separate from the user-facing CLI.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/termbacktime/termbacktime/internal/buildenv"
)

func run(args []string) error {
	if len(args) != 2 || (args[0] != "build" && args[0] != "site-url") {
		return fmt.Errorf("usage: build-go build|site-url dev|prod")
	}
	site, err := buildenv.SiteURL(".", args[1], os.LookupEnv)
	if err != nil {
		return err
	}
	if args[0] == "site-url" {
		fmt.Println(site)
		return nil
	}
	if site == "" {
		field := "SITE_URL"
		if args[1] == "dev" {
			field += "_DEV"
		}
		return fmt.Errorf("set %s in .env or the shell environment before building", field)
	}
	binary := filepath.Join("builds", "termbacktime")
	if args[1] == "dev" {
		binary += "-dev"
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		return err
	}
	command := exec.Command("go", "build", "-trimpath", "-ldflags", "-X github.com/termbacktime/termbacktime/internal/config.EmbeddedSiteURL="+site, "-o", binary, ".")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return err
	}
	fmt.Printf("Built %s for %s\n", binary, args[1])
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
