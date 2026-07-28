package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, flagName := range []string{"-browser", "-demo", "-no-open"} {
		if !strings.Contains(stderr.String(), flagName) {
			t.Fatalf("help output = %q, want %s flag", stderr.String(), flagName)
		}
	}
}

func TestParseArgumentsSelectsInterface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		browser   bool
		demo      bool
		noOpen    bool
	}{
		{
			name: "terminal ui by default",
		},
		{
			name:      "browser interface",
			arguments: []string{"--browser"},
			browser:   true,
		},
		{
			name:      "browser interface without automatic opening",
			arguments: []string{"--browser", "--no-open"},
			browser:   true,
			noOpen:    true,
		},
		{
			name:      "terminal demo",
			arguments: []string{"--demo"},
			demo:      true,
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
			if options.Browser != test.browser {
				t.Fatalf(
					"Browser = %t, want %t",
					options.Browser,
					test.browser,
				)
			}
			if options.Demo != test.demo {
				t.Fatalf(
					"Demo = %t, want %t",
					options.Demo,
					test.demo,
				)
			}
			if options.NoOpen != test.noOpen {
				t.Fatalf(
					"NoOpen = %t, want %t",
					options.NoOpen,
					test.noOpen,
				)
			}
		})
	}
}

func TestParseArgumentsRejectsReplacedFlags(t *testing.T) {
	t.Parallel()

	for _, flagName := range []string{"--ui", "--no-browser"} {
		t.Run(flagName, func(t *testing.T) {
			t.Parallel()

			_, _, err := parseArguments(
				[]string{flagName},
				&bytes.Buffer{},
			)
			if err == nil {
				t.Fatal("parseArguments() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("parseArguments() error = %q", err)
			}
		})
	}
}

func TestRunRejectsNoOpenWithoutBrowser(t *testing.T) {
	t.Parallel()

	err := run(
		context.Background(),
		[]string{"--no-open"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("run() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "--no-open requires --browser") {
		t.Fatalf("run() error = %q", err)
	}
}

func TestRunUsesTerminalInterfaceByDefault(t *testing.T) {
	t.Parallel()

	err := run(
		context.Background(),
		nil,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("run() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "terminal UI requires") {
		t.Fatalf("run() error = %q", err)
	}
}

func TestRunStartsDemoWithoutGitHubCredentials(t *testing.T) {
	t.Setenv("PATH", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dataDir := filepath.Join(t.TempDir(), "workbench-cache")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(
		ctx,
		[]string{
			"--demo",
			"--browser",
			"--no-open",
			"--data-dir",
			dataDir,
		},
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(
		got,
		"GitHub Workbench is running for demo-user@github.example",
	) {
		t.Fatalf("stdout = %q, want demo account", got)
	}
	if got := stdout.String(); !strings.Contains(got, "/session/") {
		t.Fatalf("stdout = %q, want authenticated session URL", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("demo data directory stat error = %v, want not found", err)
	}
}
