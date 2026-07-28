package app

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestReadKeyringTokenUsesActiveSecureStorage(t *testing.T) {
	t.Parallel()

	environment := []string{
		"PATH=/usr/bin",
		"GH_HOST=github.example.com",
		"GH_TOKEN=environment-token",
		"GITHUB_TOKEN=environment-token",
		"GH_ENTERPRISE_TOKEN=environment-token",
		"GITHUB_ENTERPRISE_TOKEN=environment-token",
	}
	var (
		gotArguments   []string
		gotEnvironment []string
	)
	token, err := readKeyringToken(
		context.Background(),
		"github.example.com",
		environment,
		func(
			_ context.Context,
			arguments []string,
			filteredEnvironment []string,
		) ([]byte, error) {
			gotArguments = slices.Clone(arguments)
			gotEnvironment = slices.Clone(filteredEnvironment)
			return []byte("keyring-token\n"), nil
		},
	)
	if err != nil {
		t.Fatalf("readKeyringToken() error = %v", err)
	}
	if token != "keyring-token" {
		t.Fatalf("readKeyringToken() = %q, want keyring-token", token)
	}
	wantArguments := []string{
		"auth",
		"token",
		"--secure-storage",
		"--hostname",
		"github.example.com",
	}
	if !slices.Equal(gotArguments, wantArguments) {
		t.Fatalf("command arguments = %v, want %v", gotArguments, wantArguments)
	}
	wantEnvironment := []string{
		"PATH=/usr/bin",
		"GH_HOST=github.example.com",
	}
	if !slices.Equal(gotEnvironment, wantEnvironment) {
		t.Fatalf(
			"command environment = %v, want %v",
			gotEnvironment,
			wantEnvironment,
		)
	}
}

func TestDatabaseFilenameIsScopedByAccount(t *testing.T) {
	t.Parallel()

	first := databaseFilename("github.com", "octocat")
	second := databaseFilename("github.com", "hubot")
	if first == second {
		t.Fatalf("database filenames match across accounts: %q", first)
	}
	if first != databaseFilename("github.com", "octocat") {
		t.Fatalf("database filename is unstable: %q", first)
	}
}

func TestValidateTUIStreamsRejectsNonTerminalStreams(t *testing.T) {
	t.Parallel()

	err := validateTUIStreams(&bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("validateTUIStreams() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("validateTUIStreams() error = %q", err)
	}
}
