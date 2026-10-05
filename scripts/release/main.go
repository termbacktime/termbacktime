// Release packaging remains separate from the user-facing CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/termbacktime/termbacktime/internal/release"
)

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: release [v1.x.y[-prerelease]]")
	}
	version := ""
	if len(args) == 1 {
		version = args[0]
	}
	return release.Build(ctx, ".", version, output)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
