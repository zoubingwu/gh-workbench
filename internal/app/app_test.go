package app

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
	"github.com/zoubingwu/gh-workbench/internal/store"
	"github.com/zoubingwu/gh-workbench/internal/syncer"
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

func TestTerminalSnapshotSourceObservesAndUpdatesNotifications(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	database, err := store.Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if err := database.EnsureAccount(ctx, "github.com", time.Now().UTC()); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}

	runner := syncer.New(database, nil, "github.com", "octocat", 1, nil)
	var (
		observed model.Snapshot
		updates  int
	)
	source := &terminalSnapshotSource{
		database:               database,
		runner:                 runner,
		host:                   "github.com",
		viewer:                 "octocat",
		notificationsSupported: true,
		observeNotifications: func(_ context.Context, snapshot model.Snapshot) {
			observed = snapshot
		},
		signalSnapshotUpdate: func() {
			updates++
		},
	}

	snapshot, err := source.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !snapshot.Notifications.Supported {
		t.Fatal("Snapshot().Notifications.Supported = false, want true")
	}
	if observed.Notifications != snapshot.Notifications {
		t.Fatalf(
			"observed notifications = %#v, want %#v",
			observed.Notifications,
			snapshot.Notifications,
		)
	}

	enabled := true
	if err := source.UpdateNotificationPreferences(
		ctx,
		model.NotificationPreferencesUpdate{Enabled: &enabled},
	); err != nil {
		t.Fatalf("UpdateNotificationPreferences() error = %v", err)
	}
	onlyMine := false
	if err := source.UpdateNotificationPreferences(
		ctx,
		model.NotificationPreferencesUpdate{OnlyMyPullRequests: &onlyMine},
	); err != nil {
		t.Fatalf("UpdateNotificationPreferences(Only my PRs) error = %v", err)
	}
	preferences, err := database.NotificationPreferences(ctx)
	if err != nil {
		t.Fatalf("NotificationPreferences() error = %v", err)
	}
	if !preferences.Enabled || preferences.OnlyMyPullRequests {
		t.Fatalf(
			"persisted notification preferences = %#v, want enabled and all PRs",
			preferences,
		)
	}
	if updates != 2 {
		t.Fatalf("snapshot update count = %d, want 2", updates)
	}
}
