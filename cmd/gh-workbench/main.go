package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zoubingwu/gh-workbench/internal/app"
)

func main() {
	var options app.Options
	flag.StringVar(
		&options.DataDir,
		"data-dir",
		"",
		"directory for the local SQLite cache",
	)
	flag.BoolVar(
		&options.NoBrowser,
		"no-browser",
		false,
		"print the authenticated local URL instead of opening a browser",
	)
	flag.Parse()

	options.Stdout = os.Stdout
	options.Stderr = os.Stderr

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := app.Run(ctx, options); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gh workbench: %v\n", err)
		os.Exit(1)
	}
}
