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
	options, showVersion, err := parseArguments(arguments, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if showVersion {
		_, err := fmt.Fprintf(stdout, "gh-workbench %s\n", version)
		return err
	}

	options.Stdin = os.Stdin
	options.Stdout = stdout
	options.Stderr = stderr

	return app.Run(ctx, options)
}

func parseArguments(
	arguments []string,
	stderr io.Writer,
) (app.Options, bool, error) {
	var (
		options     app.Options
		showVersion bool
	)
	flags := flag.NewFlagSet("gh-workbench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(
		&options.Browser,
		"browser",
		false,
		"use the browser interface",
	)
	flags.StringVar(
		&options.DataDir,
		"data-dir",
		"",
		"directory for the local SQLite cache",
	)
	flags.BoolVar(
		&options.NoOpen,
		"no-open",
		false,
		"print the authenticated local URL for manual opening",
	)
	flags.BoolVar(
		&showVersion,
		"version",
		false,
		"print the build version",
	)
	if err := flags.Parse(arguments); err != nil {
		return app.Options{}, false, err
	}

	return options, showVersion, nil
}
