package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zoubingwu/gh-workbench/internal/app"
)

func TestRunPrintsVersion(t *testing.T) {
	previousVersion := version
	version = "v0.1.0"
	t.Cleanup(func() {
		version = previousVersion
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"--version"},
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), "gh-workbench v0.1.0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunPrintsHelpWithoutStartingApplication(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(
		context.Background(),
		[]string{"--help"},
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "-ui string") {
		t.Fatalf("help output = %q, want --ui flag", stderr.String())
	}
}

func TestParseArgumentsSelectsUI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		expected  app.UI
	}{
		{
			name:     "browser by default",
			expected: app.UIBrowser,
		},
		{
			name:      "terminal ui",
			arguments: []string{"--ui", "tui"},
			expected:  app.UITUI,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options, showVersion, err := parseArguments(
				test.arguments,
				&bytes.Buffer{},
			)
			if err != nil {
				t.Fatalf("parseArguments() error = %v", err)
			}
			if showVersion {
				t.Fatal("showVersion = true, want false")
			}
			if options.UI != test.expected {
				t.Fatalf("UI = %q, want %q", options.UI, test.expected)
			}
		})
	}
}

func TestParseArgumentsRejectsInvalidUI(t *testing.T) {
	t.Parallel()

	_, _, err := parseArguments(
		[]string{"--ui", "desktop"},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("parseArguments() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), `invalid ui "desktop"`) {
		t.Fatalf("parseArguments() error = %q", err)
	}
}

func TestRunRejectsNoBrowserWithTUI(t *testing.T) {
	t.Parallel()

	err := run(
		context.Background(),
		[]string{"--ui", "tui", "--no-browser"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("run() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "--no-browser and --ui tui") {
		t.Fatalf("run() error = %q", err)
	}
}
