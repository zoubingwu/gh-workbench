package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/zoubingwu/gh-workbench/internal/app"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gh workbench: %v\n", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	var options app.Options
	var showVersion bool
	flags := flag.NewFlagSet("gh-workbench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(
		&options.DataDir,
		"data-dir",
		"",
		"directory for the local SQLite cache",
	)
	flags.BoolVar(
		&options.NoBrowser,
		"no-browser",
		false,
		"print the authenticated local URL instead of opening a browser",
	)
	flags.BoolVar(
		&showVersion,
		"version",
		false,
		"print the build version",
	)
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if showVersion {
		_, err := fmt.Fprintf(stdout, "gh-workbench %s\n", version)
		return err
	}

	options.Stdout = stdout
	options.Stderr = stderr

	return app.Run(ctx, options)
}
