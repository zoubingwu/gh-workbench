package main

import (
	"bytes"
	"context"
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
