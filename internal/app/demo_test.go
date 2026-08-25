package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestDemoBackendSnapshotCoversScreenshotStates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	snapshot, err := newDemoBackend().Snapshot(
		context.Background(),
		demoHost,
		false,
		now,
	)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Host != demoHost || snapshot.Viewer != demoViewer {
		t.Fatalf(
			"snapshot identity = %s@%s, want %s@%s",
			snapshot.Viewer,
			snapshot.Host,
			demoViewer,
			demoHost,
		)
	}
	if snapshot.RepositoryCount != 3 {
		t.Fatalf(
			"repository count = %d, want 3",
			snapshot.RepositoryCount,
		)
	}
	if len(snapshot.Items) != 7 {
		t.Fatalf("item count = %d, want 7", len(snapshot.Items))
	}
	if !snapshot.Notifications.Enabled ||
		!snapshot.Notifications.OnlyMine {
		t.Fatalf(
				"notification preferences = %#v, want enabled and only mine",
			snapshot.Notifications,
		)
	}

	var (
		hasApproved         bool
		hasChangesRequested bool
		hasDraft            bool
		hasIssueLabels      bool
		hasActivity         bool
		hasCommitActivity   bool
		hasLocalAgent       bool
		hasReactions        bool
		hasInactive         bool
	)
	for _, item := range snapshot.Items {
		if !strings.HasPrefix(item.URL, "https://github.example/") {
			t.Fatalf("item URL = %q, want reserved demo host", item.URL)
		}
		if item.LatestActivity != nil {
			hasActivity = true
			hasCommitActivity = hasCommitActivity ||
				item.LatestActivity.Kind == "commit"
		}
		hasLocalAgent = hasLocalAgent || item.LocalAgentActivity != nil
		if len(item.Reactions) > 0 {
			hasReactions = true
		}
		if now.Sub(item.UpdatedAt) > 30*24*time.Hour {
			hasInactive = true
		}

		switch item.Kind {
		case model.ItemKindIssue:
			hasIssueLabels = hasIssueLabels || len(item.Labels) > 0
		case model.ItemKindPullRequest:
			if item.Author != demoViewer {
				t.Fatalf(
					"pull request author = %q, want %q",
					item.Author,
					demoViewer,
				)
			}
			hasApproved = hasApproved ||
				item.ReviewDecision == "APPROVED"
			hasChangesRequested = hasChangesRequested ||
				item.ReviewDecision == "CHANGES_REQUESTED"
			hasDraft = hasDraft || item.IsDraft
		default:
			t.Fatalf("unexpected item kind %q", item.Kind)
		}
	}

	checks := []struct {
		name  string
		found bool
	}{
		{name: "approved pull request", found: hasApproved},
		{name: "changes requested pull request", found: hasChangesRequested},
		{name: "draft pull request", found: hasDraft},
		{name: "issue labels", found: hasIssueLabels},
		{name: "latest activity", found: hasActivity},
		{name: "commit activity", found: hasCommitActivity},
		{name: "local agent activity", found: hasLocalAgent},
		{name: "reactions", found: hasReactions},
		{name: "inactive item", found: hasInactive},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()
			if !check.found {
				t.Fatalf("demo snapshot is missing %s", check.name)
			}
		})
	}
}
