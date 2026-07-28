package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestStoreReconcilesRelevantOpenItemsAndReactions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}

	items := []model.WorkItem{
		{
			RepositoryKey:  "github.com/acme/rocket",
			Number:         7,
			Kind:           model.ItemKindPullRequest,
			Title:          "Ship the rocket",
			URL:            "https://github.com/acme/rocket/pull/7",
			State:          "open",
			Author:         "octocat",
			CreatedAt:      now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			ReviewDecision: "review_required",
			MergeState:     "blocked",
			NeedsReview:    true,
			Additions:      42,
			Deletions:      7,
		},
		{
			RepositoryKey: "github.com/octocat/satellite",
			Number:        3,
			Kind:          model.ItemKindIssue,
			Title:         "Track fuel",
			URL:           "https://github.com/octocat/satellite/issues/3",
			State:         "open",
			Author:        "hubot",
			CreatedAt:     now.Add(-72 * time.Hour),
			UpdatedAt:     now.Add(-24 * time.Hour),
			Labels: []model.Label{
				{Name: "bug", Color: "d73a4a"},
				{Name: "priority: high", Color: "b60205"},
			},
		},
	}

	changed, err := database.ReplaceRelevantOpenItems(ctx, host, items, now)
	if err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	if !changed {
		t.Fatal("ReplaceRelevantOpenItems() changed = false, want true")
	}
	changed, err = database.ReplaceRelevantOpenItems(ctx, host, items, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second ReplaceRelevantOpenItems() error = %v", err)
	}
	if changed {
		t.Fatal("second ReplaceRelevantOpenItems() changed = true, want false")
	}
	items[1].Labels[0].Color = "cf222e"
	changed, err = database.ReplaceRelevantOpenItems(
		ctx,
		host,
		items,
		now.Add(90*time.Second),
	)
	if err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() after label change error = %v", err)
	}
	if !changed {
		t.Fatal("ReplaceRelevantOpenItems() after label change = false, want true")
	}

	reactions := []model.Reaction{
		{
			ID:        42,
			Content:   "eyes",
			User:      "copilot-pull-request-reviewer[bot]",
			CreatedAt: now.Add(-30 * time.Minute),
		},
	}
	var applied bool
	changed, applied, err = database.ReplaceReactions(
		ctx,
		"github.com/acme/rocket",
		7,
		0,
		reactions,
	)
	if err != nil {
		t.Fatalf("ReplaceReactions() error = %v", err)
	}
	if !changed {
		t.Fatal("ReplaceReactions() changed = false, want true")
	}
	if !applied {
		t.Fatal("ReplaceReactions() applied = false, want true")
	}

	snapshot, err := database.Snapshot(ctx, host, true, now)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !snapshot.Sync.Running {
		t.Fatal("Snapshot().Sync.Running = false, want true")
	}
	if snapshot.RepositoryCount != 2 {
		t.Fatalf("Snapshot().RepositoryCount = %d, want 2", snapshot.RepositoryCount)
	}
	if len(snapshot.Items) != 2 {
		t.Fatalf("len(Snapshot().Items) = %d, want 2", len(snapshot.Items))
	}

	byURL := make(map[string]model.WorkItem, len(snapshot.Items))
	for _, item := range snapshot.Items {
		byURL[item.URL] = item
	}
	pullRequest := byURL["https://github.com/acme/rocket/pull/7"]
	if pullRequest.Repository != "acme/rocket" {
		t.Fatalf("pull request repository = %q, want acme/rocket", pullRequest.Repository)
	}
	if pullRequest.Additions != 42 || pullRequest.Deletions != 7 {
		t.Fatalf(
			"pull request diff = +%d -%d, want +42 -7",
			pullRequest.Additions,
			pullRequest.Deletions,
		)
	}
	if !pullRequest.NeedsReview || pullRequest.ReviewDecision != "review_required" {
		t.Fatalf("pull request review fields = %#v", pullRequest)
	}
	if len(pullRequest.Reactions) != 1 ||
		pullRequest.Reactions[0].Content != "eyes" {
		t.Fatalf("pull request reactions = %#v, want eyes", pullRequest.Reactions)
	}
	issue := byURL["https://github.com/octocat/satellite/issues/3"]
	wantLabels := []model.Label{
		{Name: "bug", Color: "cf222e"},
		{Name: "priority: high", Color: "b60205"},
	}
	if !slices.Equal(issue.Labels, wantLabels) {
		t.Fatalf("issue labels = %#v, want %#v", issue.Labels, wantLabels)
	}

	due, err := database.ListDueResources(ctx, host, now.Add(time.Second), 10)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	if len(due) != 4 {
		t.Fatalf("len(ListDueResources()) = %d, want 4", len(due))
	}
	counts := make(map[model.ResourceKind]int)
	for _, resource := range due {
		counts[resource.Kind]++
	}
	if counts[model.ResourceKindWorkItems] != 1 ||
		counts[model.ResourceKindActivity] != 2 ||
		counts[model.ResourceKindReactions] != 1 {
		t.Fatalf("due resource counts = %#v", counts)
	}

	changed, err = database.ReplaceRelevantOpenItems(
		ctx,
		host,
		items[1:],
		now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("remove pull request: %v", err)
	}
	if !changed {
		t.Fatal("remove pull request changed = false, want true")
	}
	due, err = database.ListDueResources(ctx, host, now.Add(3*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDueResources() after removal error = %v", err)
	}
	reactionResourceFound := false
	for _, resource := range due {
		if resource.Kind == model.ResourceKindReactions {
			reactionResourceFound = true
		}
	}
	if !reactionResourceFound {
		t.Fatal("reaction resource was removed after one missing search result")
	}
	snapshot, err = database.Snapshot(ctx, host, false, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Snapshot() after removal error = %v", err)
	}
	if len(snapshot.Items) != 1 {
		t.Fatalf("visible items after removal = %d, want 1", len(snapshot.Items))
	}

	for miss := 2; miss <= missingPollsBeforeDelete; miss++ {
		changed, err = database.ReplaceRelevantOpenItems(
			ctx,
			host,
			items[1:],
			now.Add(time.Duration(miss+1)*time.Minute),
		)
		if err != nil {
			t.Fatalf("missing poll %d error: %v", miss, err)
		}
		if changed {
			t.Fatalf("missing poll %d changed = true, want false", miss)
		}
	}
	due, err = database.ListDueResources(ctx, host, now.Add(10*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDueResources() after confirmed removal error = %v", err)
	}
	for _, resource := range due {
		if resource.Kind == model.ResourceKindReactions {
			t.Fatalf("reaction resource remained after confirmed removal: %#v", resource)
		}
		if resource.Key == model.ActivityResourceKey(
			"github.com/acme/rocket",
			7,
		) {
			t.Fatalf("activity resource remained after confirmed removal: %#v", resource)
		}
	}

	changed, applied, err = database.ReplaceReactions(
		ctx,
		"github.com/acme/rocket",
		7,
		0,
		reactions,
	)
	if err != nil {
		t.Fatalf("stale ReplaceReactions() error = %v", err)
	}
	if changed {
		t.Fatal("stale ReplaceReactions() changed = true, want false")
	}
	if applied {
		t.Fatal("stale ReplaceReactions() applied = true, want false")
	}
}

func TestStoreKeepsSameNumberItemsFromDifferentRepositories(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	items := []model.WorkItem{
		{
			RepositoryKey: "github.com/acme/api",
			Number:        12,
			Kind:          model.ItemKindIssue,
			Title:         "Track API errors",
			URL:           "https://github.com/acme/api/issues/12",
			State:         "open",
			Author:        "octocat",
			CreatedAt:     now.Add(-time.Hour),
			UpdatedAt:     now,
		},
		{
			RepositoryKey: "github.com/acme/web",
			Number:        12,
			Kind:          model.ItemKindIssue,
			Title:         "Track UI errors",
			URL:           "https://github.com/acme/web/issues/12",
			State:         "open",
			Author:        "octocat",
			CreatedAt:     now.Add(-time.Hour),
			UpdatedAt:     now.Add(-time.Minute),
		},
	}
	if _, err := database.ReplaceRelevantOpenItems(ctx, host, items, now); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	snapshot, err := database.Snapshot(ctx, host, false, now)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Items) != 2 {
		t.Fatalf("len(Snapshot().Items) = %d, want 2", len(snapshot.Items))
	}
	if snapshot.RepositoryCount != 2 {
		t.Fatalf("Snapshot().RepositoryCount = %d, want 2", snapshot.RepositoryCount)
	}
}

func TestStorePersistsLatestActivityAcrossDiscoveryRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	item := model.WorkItem{
		NodeID:        "I_kwDOExample",
		RepositoryKey: "github.com/acme/api",
		Number:        12,
		Kind:          model.ItemKindPullRequest,
		Title:         "Track API errors",
		URL:           "https://github.com/acme/api/pull/12",
		State:         "open",
		Author:        "octocat",
		CreatedAt:     now.Add(-time.Hour),
		UpdatedAt:     now,
	}
	if _, err := database.ReplaceRelevantOpenItems(
		ctx,
		host,
		[]model.WorkItem{item},
		now,
	); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}

	due, err := database.ListDueResources(ctx, host, now, 10)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	var activityResource model.PollResource
	for _, resource := range due {
		if resource.Kind == model.ResourceKindActivity {
			activityResource = resource
		}
	}
	if activityResource.Key != model.ActivityResourceKey(item.RepositoryKey, item.Number) {
		t.Fatalf(
			"activity resource key = %q, want %q",
			activityResource.Key,
			model.ActivityResourceKey(item.RepositoryKey, item.Number),
		)
	}

	activity := &model.Activity{
		Kind:       "comment",
		Actor:      "reviewer",
		BodyText:   "Please cover the retry case.",
		OccurredAt: now.Add(-time.Minute),
		URL:        "https://github.com/acme/api/pull/12#issuecomment-1",
	}
	reviewComment := &model.Activity{
		Kind:       "review_comment",
		Actor:      "reviewer",
		BodyText:   "Please rename this value.",
		OccurredAt: now.Add(-2 * time.Minute),
		URL:        "https://github.com/acme/api/pull/12#discussion_r1",
	}
	changed, applied, err := database.ReplaceActivity(
		ctx,
		item.RepositoryKey,
		item.Number,
		activityResource.Revision,
		activity,
		reviewComment,
	)
	if err != nil {
		t.Fatalf("ReplaceActivity() error = %v", err)
	}
	if !changed || !applied {
		t.Fatalf(
			"ReplaceActivity() = changed %t, applied %t; want true, true",
			changed,
			applied,
		)
	}

	if _, err := database.ReplaceRelevantOpenItems(
		ctx,
		host,
		[]model.WorkItem{item},
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("refresh ReplaceRelevantOpenItems() error = %v", err)
	}
	snapshot, err := database.Snapshot(ctx, host, false, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Items) != 1 {
		t.Fatalf("len(Snapshot().Items) = %d, want 1", len(snapshot.Items))
	}
	got := snapshot.Items[0]
	if got.NodeID != item.NodeID {
		t.Fatalf("Snapshot().Items[0].NodeID = %q, want %q", got.NodeID, item.NodeID)
	}
	if got.LatestActivity == nil || *got.LatestActivity != *activity {
		t.Fatalf(
			"Snapshot().Items[0].LatestActivity = %#v, want %#v",
			got.LatestActivity,
			activity,
		)
	}
	due, err = database.ListDueResources(ctx, host, now.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDueResources() after refresh error = %v", err)
	}
	for _, resource := range due {
		if resource.Kind != model.ResourceKindActivity {
			continue
		}
		if resource.LatestReviewComment == nil ||
			*resource.LatestReviewComment != *reviewComment {
			t.Fatalf(
				"LatestReviewComment = %#v, want %#v",
				resource.LatestReviewComment,
				reviewComment,
			)
		}
		return
	}
	t.Fatal("activity resource missing after discovery refresh")
}

func TestStoreRejectsClosedSearchResult(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Now().UTC()
	if err := database.EnsureAccount(ctx, "github.com", now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	_, err = database.ReplaceRelevantOpenItems(
		ctx,
		"github.com",
		[]model.WorkItem{
			{
				RepositoryKey: "github.com/acme/api",
				Number:        1,
				Kind:          model.ItemKindIssue,
				Title:         "Closed",
				URL:           "https://github.com/acme/api/issues/1",
				State:         "closed",
				Author:        "octocat",
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		},
		now,
	)
	if err == nil || !strings.Contains(err.Error(), "non-open") {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v, want non-open error", err)
	}
}

func TestEnsureAccountRemovesLegacyRepositoryInventory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	if _, err := database.db.ExecContext(
		ctx,
		`INSERT INTO poll_resources (
			resource_key,
			repository,
			kind,
			interval_ns,
			next_poll_at,
			resource_updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		"github.com/acme/api:open",
		"github.com/acme/api",
		"repository",
		initialInterval.Nanoseconds(),
		now.UnixNano(),
		now.UnixNano(),
	); err != nil {
		t.Fatalf("insert legacy poll resource: %v", err)
	}
	if _, err := database.db.ExecContext(
		ctx,
		`INSERT INTO work_items (
			repository,
			number,
			kind,
			title,
			url,
			state,
			author,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"github.com/acme/api",
		1,
		model.ItemKindIssue,
		"Legacy item",
		"https://github.com/acme/api/issues/1",
		"open",
		"octocat",
		now.UnixNano(),
		now.UnixNano(),
	); err != nil {
		t.Fatalf("insert legacy work item: %v", err)
	}

	if err := database.EnsureAccount(ctx, "github.com", now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	snapshot, err := database.Snapshot(ctx, "github.com", false, now)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Items) != 0 {
		t.Fatalf("legacy snapshot items = %d, want 0", len(snapshot.Items))
	}
	due, err := database.ListDueResources(ctx, "github.com", now, 10)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	if len(due) != 1 || due[0].Kind != model.ResourceKindWorkItems {
		t.Fatalf("due resources = %#v, want one work-items resource", due)
	}
}

func TestForceDueRefreshesSearchAndReactions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	item := model.WorkItem{
		RepositoryKey: "github.com/acme/api",
		Number:        7,
		Kind:          model.ItemKindPullRequest,
		Title:         "Ship",
		URL:           "https://github.com/acme/api/pull/7",
		State:         "open",
		Author:        "octocat",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if _, err := database.ReplaceRelevantOpenItems(ctx, host, []model.WorkItem{item}, now); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	due, err := database.ListDueResources(ctx, host, now.Add(time.Second), 10)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	for _, resource := range due {
		resource.NextPollAt = now.Add(time.Hour)
		if err := database.SavePollResource(ctx, resource); err != nil {
			t.Fatalf("SavePollResource() error = %v", err)
		}
	}

	if err := database.ForceDue(ctx, host, now.Add(time.Minute)); err != nil {
		t.Fatalf("ForceDue() error = %v", err)
	}
	due, err = database.ListDueResources(ctx, host, now.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDueResources() after ForceDue error = %v", err)
	}
	if len(due) != 3 {
		t.Fatalf("forced resources = %#v, want search, activity, and reaction resources", due)
	}
	if due[0].Kind != model.ResourceKindWorkItems ||
		due[1].Kind != model.ResourceKindActivity ||
		due[2].Kind != model.ResourceKindReactions {
		t.Fatalf(
			"forced resource kinds = %q, %q, %q",
			due[0].Kind,
			due[1].Kind,
			due[2].Kind,
		)
	}
}

func TestSnapshotReportsActivityPollError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	item := model.WorkItem{
		NodeID:        "I_kwDOExample",
		RepositoryKey: "github.com/acme/api",
		Number:        7,
		Kind:          model.ItemKindIssue,
		Title:         "Track retries",
		URL:           "https://github.com/acme/api/issues/7",
		State:         "open",
		Author:        "octocat",
		CreatedAt:     now.Add(-time.Hour),
		UpdatedAt:     now,
	}
	if _, err := database.ReplaceRelevantOpenItems(
		ctx,
		host,
		[]model.WorkItem{item},
		now,
	); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	resources, err := database.ListDueResources(ctx, host, now, 10)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	for _, resource := range resources {
		if resource.Kind != model.ResourceKindActivity {
			continue
		}
		resource.LastError = "fetch latest activity"
		if err := database.SavePollResource(ctx, resource); err != nil {
			t.Fatalf("SavePollResource() error = %v", err)
		}
	}

	snapshot, err := database.Snapshot(ctx, host, false, now)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Sync.Error != "acme/api: fetch latest activity" {
		t.Fatalf(
			"Snapshot().Sync.Error = %q, want acme/api: fetch latest activity",
			snapshot.Sync.Error,
		)
	}
}

func TestStaleReactionPollDoesNotOverwriteHotReset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	item := model.WorkItem{
		RepositoryKey: "github.com/acme/api",
		Number:        7,
		Kind:          model.ItemKindPullRequest,
		Title:         "Ship",
		URL:           "https://github.com/acme/api/pull/7",
		State:         "open",
		Author:        "octocat",
		CreatedAt:     now.Add(-time.Hour),
		UpdatedAt:     now.Add(-time.Minute),
	}
	if _, err := database.ReplaceRelevantOpenItems(ctx, host, []model.WorkItem{item}, now); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	due, err := database.ListDueResources(ctx, host, now.Add(time.Second), 10)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	var stale model.PollResource
	for _, resource := range due {
		if resource.Kind == model.ResourceKindReactions {
			stale = resource
		}
	}
	if stale.Key == "" {
		t.Fatal("reaction resource missing")
	}
	initialReactions := []model.Reaction{
		{ID: 1, Content: "eyes", User: "reviewer", CreatedAt: now},
	}
	changed, applied, err := database.ReplaceReactions(
		ctx,
		item.RepositoryKey,
		item.Number,
		stale.Revision,
		initialReactions,
	)
	if err != nil {
		t.Fatalf("seed reactions error: %v", err)
	}
	if !changed || !applied {
		t.Fatalf("seed reactions = changed %t, applied %t; want true, true", changed, applied)
	}

	item.UpdatedAt = now.Add(time.Minute)
	if _, err := database.ReplaceRelevantOpenItems(
		ctx,
		host,
		[]model.WorkItem{item},
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("hot reset error = %v", err)
	}
	staleReactions := []model.Reaction{
		{ID: 2, Content: "rocket", User: "reviewer", CreatedAt: now.Add(time.Minute)},
	}
	changed, applied, err = database.ReplaceReactions(
		ctx,
		item.RepositoryKey,
		item.Number,
		stale.Revision,
		staleReactions,
	)
	if err != nil {
		t.Fatalf("stale reaction replacement error: %v", err)
	}
	if changed || applied {
		t.Fatalf(
			"stale reaction replacement = changed %t, applied %t; want false, false",
			changed,
			applied,
		)
	}
	stale.NextPollAt = now.Add(24 * time.Hour)
	if err := database.SavePollResource(ctx, stale); err != nil {
		t.Fatalf("SavePollResource(stale) error = %v", err)
	}

	due, err = database.ListDueResources(ctx, host, now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListDueResources() after hot reset error = %v", err)
	}
	found := false
	for _, resource := range due {
		if resource.Key == stale.Key {
			found = true
		}
	}
	if !found {
		t.Fatal("hot reaction resource was overwritten by stale completion")
	}
	snapshot, err := database.Snapshot(ctx, host, false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Snapshot() after stale replacement error = %v", err)
	}
	if len(snapshot.Items) != 1 ||
		len(snapshot.Items[0].Reactions) != 1 ||
		snapshot.Items[0].Reactions[0].Content != "eyes" {
		t.Fatalf("reactions after stale replacement = %#v, want eyes", snapshot.Items)
	}
}

func TestStaleActivityPollDoesNotOverwriteHotReset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	item := model.WorkItem{
		NodeID:        "I_kwDOExample",
		RepositoryKey: "github.com/acme/api",
		Number:        7,
		Kind:          model.ItemKindIssue,
		Title:         "Track retries",
		URL:           "https://github.com/acme/api/issues/7",
		State:         "open",
		Author:        "octocat",
		CreatedAt:     now.Add(-time.Hour),
		UpdatedAt:     now.Add(-time.Minute),
	}
	if _, err := database.ReplaceRelevantOpenItems(
		ctx,
		host,
		[]model.WorkItem{item},
		now,
	); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	due, err := database.ListDueResources(ctx, host, now, 10)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	var stale model.PollResource
	for _, resource := range due {
		if resource.Kind == model.ResourceKindActivity {
			stale = resource
		}
	}
	if stale.Key == "" {
		t.Fatal("activity resource missing")
	}
	initial := &model.Activity{
		Kind:       "comment",
		Actor:      "reviewer",
		BodyText:   "Initial feedback",
		OccurredAt: now,
		URL:        "https://github.com/acme/api/issues/7#issuecomment-1",
	}
	changed, applied, err := database.ReplaceActivity(
		ctx,
		item.RepositoryKey,
		item.Number,
		stale.Revision,
		initial,
		nil,
	)
	if err != nil {
		t.Fatalf("seed activity error: %v", err)
	}
	if !changed || !applied {
		t.Fatalf("seed activity = changed %t, applied %t; want true, true", changed, applied)
	}

	item.UpdatedAt = now.Add(time.Minute)
	if _, err := database.ReplaceRelevantOpenItems(
		ctx,
		host,
		[]model.WorkItem{item},
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("hot reset error = %v", err)
	}
	staleActivity := &model.Activity{
		Kind:       "comment",
		Actor:      "reviewer",
		BodyText:   "Stale feedback",
		OccurredAt: now.Add(time.Minute),
		URL:        "https://github.com/acme/api/issues/7#issuecomment-2",
	}
	changed, applied, err = database.ReplaceActivity(
		ctx,
		item.RepositoryKey,
		item.Number,
		stale.Revision,
		staleActivity,
		nil,
	)
	if err != nil {
		t.Fatalf("stale activity replacement error: %v", err)
	}
	if changed || applied {
		t.Fatalf(
			"stale activity replacement = changed %t, applied %t; want false, false",
			changed,
			applied,
		)
	}

	snapshot, err := database.Snapshot(ctx, host, false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Snapshot() after stale replacement error = %v", err)
	}
	if len(snapshot.Items) != 1 ||
		snapshot.Items[0].LatestActivity == nil ||
		*snapshot.Items[0].LatestActivity != *initial {
		t.Fatalf(
			"activity after stale replacement = %#v, want %#v",
			snapshot.Items,
			initial,
		)
	}
}

func TestOpenMigratesWorkItemColumns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE work_items (
			repository TEXT NOT NULL,
			number INTEGER NOT NULL,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			state TEXT NOT NULL,
			author TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (repository, number)
		)
	`); err != nil {
		t.Fatalf("create legacy work_items: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE poll_resources (
			resource_key TEXT PRIMARY KEY,
			repository TEXT NOT NULL,
			kind TEXT NOT NULL,
			number INTEGER NOT NULL DEFAULT 0,
			etag TEXT NOT NULL DEFAULT '',
			interval_ns INTEGER NOT NULL,
			next_poll_at INTEGER NOT NULL,
			last_poll_at INTEGER,
			last_success_at INTEGER,
			last_changed_at INTEGER,
			resource_updated_at INTEGER NOT NULL,
			unchanged_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO poll_resources (
			resource_key,
			repository,
			kind,
			number,
			etag,
			interval_ns,
			next_poll_at,
			resource_updated_at
		) VALUES (
			'github.com/acme/api:item:12:activity',
			'github.com/acme/api',
			'activity',
			12,
			'"legacy-inline"',
			1000000000,
			0,
			0
		)
	`); err != nil {
		t.Fatalf("create legacy poll resource: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open() migration error = %v", err)
	}
	defer database.Close()

	rows, err := database.db.Query("PRAGMA table_info(work_items)")
	if err != nil {
		t.Fatalf("inspect migrated schema: %v", err)
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			position   int
			name       string
			columnType string
			notNull    int
			defaultSQL sql.NullString
			primaryKey int
		)
		if err := rows.Scan(
			&position,
			&name,
			&columnType,
			&notNull,
			&defaultSQL,
			&primaryKey,
		); err != nil {
			t.Fatalf("scan migrated schema: %v", err)
		}
		columns[name] = struct{}{}
	}
	for _, name := range []string{
		"node_id",
		"is_draft",
		"review_decision",
		"merge_state",
		"needs_review",
		"additions",
		"deletions",
		"labels_json",
		"latest_activity_json",
		"latest_review_comment_json",
		"missing_polls",
	} {
		if _, ok := columns[name]; !ok {
			t.Fatalf("migrated column %q is missing", name)
		}
	}
	var etag string
	if err := database.db.QueryRow(
		"SELECT etag FROM poll_resources WHERE resource_key = ?",
		"github.com/acme/api:item:12:activity",
	).Scan(&etag); err != nil {
		t.Fatalf("load migrated activity ETag: %v", err)
	}
	if etag != "" {
		t.Fatalf("migrated activity ETag = %q, want empty cache reset", etag)
	}
}

func TestOpenClearsActivityETagWithoutReviewCommentCache(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "interrupted-migration.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if err := database.EnsureAccount(ctx, "github.com", now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	item := model.WorkItem{
		NodeID:        "PR_interrupted",
		RepositoryKey: "github.com/acme/api",
		Number:        12,
		Kind:          model.ItemKindPullRequest,
		Title:         "Interrupted migration",
		URL:           "https://github.com/acme/api/pull/12",
		State:         "open",
		Author:        "octocat",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if _, err := database.ReplaceRelevantOpenItems(
		ctx,
		"github.com",
		[]model.WorkItem{item},
		now,
	); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	if _, err := database.db.Exec(
		`UPDATE poll_resources SET etag = ?
		WHERE resource_key = ?`,
		`"legacy-inline"`,
		model.ActivityResourceKey(item.RepositoryKey, item.Number),
	); err != nil {
		t.Fatalf("seed legacy activity ETag: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open() after interrupted migration error = %v", err)
	}
	defer reopened.Close()

	var etag string
	if err := reopened.db.QueryRow(
		"SELECT etag FROM poll_resources WHERE resource_key = ?",
		model.ActivityResourceKey(item.RepositoryKey, item.Number),
	).Scan(&etag); err != nil {
		t.Fatalf("load healed activity ETag: %v", err)
	}
	if etag != "" {
		t.Fatalf("healed activity ETag = %q, want empty cache reset", etag)
	}
}
