package app

import (
	"bytes"
	"context"
	"errors"
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

func TestSnapshotSourceAssemblesCanonicalSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []model.WorkItem
	}{
		{
			name: "empty items",
		},
		{
			name: "decorated item",
			items: []model.WorkItem{{
				Kind: model.ItemKindPullRequest,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			database := &fakeSnapshotDatabase{
				snapshot: model.Snapshot{
					Host:   "stale.example",
					Viewer: "stale-viewer",
					Notifications: model.NotificationPreferences{
						Enabled: true,
					},
					Items: tt.items,
				},
			}
			decorator := &recordingItemDecorator{}
			source := &snapshotSource{
				database:               database,
				runner:                 fakeSyncState{running: true},
				decorator:              decorator,
				host:                   "github.com",
				viewer:                 "octocat",
				notificationsSupported: true,
			}

			snapshot, err := source.Snapshot(t.Context())
			if err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			if database.scope != "github.com" || !database.running {
				t.Fatalf(
					"store snapshot scope = %q, running = %t; want github.com and true",
					database.scope,
					database.running,
				)
			}
			if database.now.IsZero() || database.now.Location() != time.UTC {
				t.Fatalf("store snapshot time = %v, want current UTC time", database.now)
			}
			if snapshot.Host != "github.com" || snapshot.Viewer != "octocat" {
				t.Fatalf(
					"snapshot identity = %s@%s, want octocat@github.com",
					snapshot.Viewer,
					snapshot.Host,
				)
			}
			if !snapshot.Notifications.Enabled ||
				!snapshot.Notifications.Supported {
				t.Fatalf(
					"snapshot notifications = %#v, want enabled and supported",
					snapshot.Notifications,
				)
			}
			if snapshot.Items == nil {
				t.Fatal("Snapshot().Items = nil, want an empty slice")
			}
			if decorator.calls != 1 {
				t.Fatalf("decorator calls = %d, want 1", decorator.calls)
			}
			if len(snapshot.Items) > 0 &&
				snapshot.Items[0].LocalAgentActivity == nil {
				t.Fatalf("decorated snapshot items = %#v", snapshot.Items)
			}
		})
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

	runner := syncer.New(database, nil, "github.com", "octocat", 1, nil, nil)
	var (
		observed model.Snapshot
		updates  int
	)
	workbenchSource := &snapshotSource{
		database:               database,
		runner:                 runner,
		host:                   "github.com",
		viewer:                 "octocat",
		notificationsSupported: true,
	}
	source := &terminalSnapshotSource{
		source: workbenchSource,
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
	if snapshot.Host != "github.com" ||
		snapshot.Viewer != "octocat" ||
		snapshot.Items == nil {
		t.Fatalf("assembled terminal snapshot = %#v", snapshot)
	}
	if observed.Host != snapshot.Host ||
		observed.Viewer != snapshot.Viewer ||
		observed.GeneratedAt != snapshot.GeneratedAt ||
		observed.Notifications != snapshot.Notifications {
		t.Fatalf(
			"observed snapshot = %#v, want %#v",
			observed,
			snapshot,
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

func TestTerminalSnapshotSourcePropagatesSourceErrors(t *testing.T) {
	t.Parallel()

	snapshotErr := errors.New("snapshot failed")
	updateErr := errors.New("update failed")
	database := &fakeSnapshotDatabase{
		snapshotErr: snapshotErr,
		updateErr:   updateErr,
	}
	var (
		observerCalls int
		updateSignals int
	)
	source := &terminalSnapshotSource{
		source: &snapshotSource{
			database: database,
			runner:   fakeSyncState{},
		},
		observeNotifications: func(context.Context, model.Snapshot) {
			observerCalls++
		},
		signalSnapshotUpdate: func() {
			updateSignals++
		},
	}

	if _, err := source.Snapshot(t.Context()); !errors.Is(err, snapshotErr) {
		t.Fatalf("Snapshot() error = %v, want %v", err, snapshotErr)
	}
	enabled := true
	err := source.UpdateNotificationPreferences(
		t.Context(),
		model.NotificationPreferencesUpdate{Enabled: &enabled},
	)
	if !errors.Is(err, updateErr) {
		t.Fatalf(
			"UpdateNotificationPreferences() error = %v, want %v",
			err,
			updateErr,
		)
	}
	if observerCalls != 0 || updateSignals != 0 {
		t.Fatalf(
			"observer calls = %d, update signals = %d; want zero",
			observerCalls,
			updateSignals,
		)
	}
}

type fakeSnapshotDatabase struct {
	snapshot    model.Snapshot
	snapshotErr error
	updateErr   error
	scope       string
	running     bool
	now         time.Time
}

func (f *fakeSnapshotDatabase) Snapshot(
	_ context.Context,
	scope string,
	running bool,
	now time.Time,
) (model.Snapshot, error) {
	f.scope = scope
	f.running = running
	f.now = now
	return f.snapshot, f.snapshotErr
}

func (f *fakeSnapshotDatabase) UpdateNotificationPreferences(
	_ context.Context,
	_ model.NotificationPreferencesUpdate,
) error {
	return f.updateErr
}

type fakeSyncState struct {
	running bool
}

func (f fakeSyncState) Running() bool {
	return f.running
}

type recordingItemDecorator struct {
	calls int
}

func (d *recordingItemDecorator) Decorate(items []model.WorkItem) {
	d.calls++
	for index := range items {
		items[index].LocalAgentActivity = &model.LocalAgentActivity{
			State:        model.LocalAgentStateWorking,
			Providers:    []string{"codex"},
			SessionCount: 1,
			Confidence:   model.LocalAgentConfidenceHeuristic,
		}
	}
}
