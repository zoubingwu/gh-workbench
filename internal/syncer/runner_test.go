package syncer

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
	"github.com/zoubingwu/gh-workbench/internal/store"
)

func TestRunnerBatchesActivitiesAndPollsReactions(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	database, err := store.Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	now := time.Now().UTC()
	host := "github.com"
	viewer := "octocat"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}

	items := []model.WorkItem{
		{
			NodeID:         "PR_kwDO_rocket_7",
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
			NeedsReview:    true,
			Additions:      42,
			Deletions:      7,
		},
		{
			NodeID:        "PR_kwDO_satellite_7",
			RepositoryKey: "github.com/octocat/satellite",
			Number:        7,
			Kind:          model.ItemKindPullRequest,
			Title:         "Launch the satellite",
			URL:           "https://github.com/octocat/satellite/pull/7",
			State:         "open",
			Author:        "hubot",
			CreatedAt:     now.Add(-72 * time.Hour),
			UpdatedAt:     now.Add(-2 * time.Hour),
			IsDraft:       true,
			Additions:     12,
			Deletions:     3,
		},
	}
	activities := map[string]*model.Activity{
		"PR_kwDO_rocket_7": {
			Kind:       "comment",
			Actor:      "reviewer",
			BodyText:   "Please cover the retry case.",
			OccurredAt: now.Add(-30 * time.Minute),
			URL:        "https://github.com/acme/rocket/pull/7#issuecomment-1",
		},
		"PR_kwDO_satellite_7": {
			Kind:       "review_approved",
			Actor:      "reviewer",
			OccurredAt: now.Add(-time.Hour),
			URL:        "https://github.com/octocat/satellite/pull/7#pullrequestreview-1",
		},
	}
	source := &fakeSource{
		items:      model.ItemsResult{Items: items},
		activities: activities,
		reactions: map[string]model.ReactionsResult{
			"github.com/acme/rocket": {
				Reactions: []model.Reaction{
					{ID: 42, Content: "eyes", User: "reviewer", CreatedAt: now},
				},
			},
			"github.com/octocat/satellite": {
				Reactions: []model.Reaction{
					{ID: 43, Content: "rocket", User: "reviewer", CreatedAt: now},
				},
			},
		},
	}

	updates := make(chan struct{}, 8)
	runner := New(database, source, host, viewer, 2, func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
	}()

	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-updates:
			snapshot, err := database.Snapshot(
				context.Background(),
				host,
				runner.Running(),
				time.Now().UTC(),
			)
			if err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			if snapshotHasActivityAndReactions(snapshot, activities) {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("Runner.Run() error = %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("Runner.Run() did not stop after cancellation")
				}
				if calls := source.activityCalls.Load(); calls != 1 {
					t.Fatalf("activity API calls = %d, want 1", calls)
				}
				return
			}
		case <-timeout.C:
			t.Fatal("runner did not publish a reaction snapshot")
		}
	}
}

func TestRunnerRecordsActivityBatchRateLimitForEveryResource(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	database, err := store.Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	now := time.Now().UTC()
	host := "github.com"
	if err := database.EnsureAccount(ctx, host, now); err != nil {
		t.Fatalf("EnsureAccount() error = %v", err)
	}
	items := []model.WorkItem{
		{
			NodeID:        "I_kwDO_api_1",
			RepositoryKey: "github.com/acme/api",
			Number:        1,
			Kind:          model.ItemKindIssue,
			Title:         "Track retries",
			URL:           "https://github.com/acme/api/issues/1",
			State:         "open",
			Author:        "octocat",
			CreatedAt:     now.Add(-time.Hour),
			UpdatedAt:     now,
		},
		{
			NodeID:        "I_kwDO_web_2",
			RepositoryKey: "github.com/acme/web",
			Number:        2,
			Kind:          model.ItemKindIssue,
			Title:         "Track timeouts",
			URL:           "https://github.com/acme/web/issues/2",
			State:         "open",
			Author:        "octocat",
			CreatedAt:     now.Add(-time.Hour),
			UpdatedAt:     now,
		},
	}
	if _, err := database.ReplaceRelevantOpenItems(ctx, host, items, now); err != nil {
		t.Fatalf("ReplaceRelevantOpenItems() error = %v", err)
	}
	deferNonActivityResources(t, database, host, now)

	retryAt := now.Add(30 * time.Minute)
	source := &fakeSource{
		activityErr: retryableError{
			message: "GitHub rate limit",
			retryAt: retryAt,
		},
	}
	updates := make(chan struct{}, 1)
	runner := New(database, source, host, "octocat", 1, func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
	}()

	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not publish activity batch errors")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Runner.Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Runner.Run() did not stop after cancellation")
	}
	if calls := source.activityCalls.Load(); calls != 1 {
		t.Fatalf("activity API calls = %d, want 1", calls)
	}

	beforeRetry, err := database.ListDueResources(
		context.Background(),
		host,
		retryAt.Add(-time.Nanosecond),
		10,
	)
	if err != nil {
		t.Fatalf("ListDueResources() before retry error = %v", err)
	}
	if len(beforeRetry) != 0 {
		t.Fatalf("resources due before rate-limit reset = %#v, want none", beforeRetry)
	}
	afterRetry, err := database.ListDueResources(
		context.Background(),
		host,
		retryAt,
		10,
	)
	if err != nil {
		t.Fatalf("ListDueResources() at retry error = %v", err)
	}
	if len(afterRetry) != 2 {
		t.Fatalf("resources due at rate-limit reset = %#v, want two", afterRetry)
	}
	for _, resource := range afterRetry {
		if resource.Kind != model.ResourceKindActivity {
			t.Fatalf("due resource kind = %q, want activity", resource.Kind)
		}
		if resource.LastError != "GitHub rate limit" {
			t.Fatalf("activity resource error = %q, want GitHub rate limit", resource.LastError)
		}
	}
}

type fakeSource struct {
	items         model.ItemsResult
	activities    map[string]*model.Activity
	reactions     map[string]model.ReactionsResult
	activityErr   error
	activityCalls atomic.Int32
}

func (f *fakeSource) FetchRelevantOpenItems(
	context.Context,
	string,
	string,
) (model.ItemsResult, error) {
	return f.items, nil
}

func (f *fakeSource) FetchReactions(
	_ context.Context,
	repository model.Repository,
	_ int,
	_ string,
) (model.ReactionsResult, error) {
	return f.reactions[repository.Key()], nil
}

func (f *fakeSource) FetchLatestActivities(
	_ context.Context,
	targets []model.ActivityTarget,
) ([]model.ActivityResult, error) {
	f.activityCalls.Add(1)
	if f.activityErr != nil {
		return nil, f.activityErr
	}
	results := make([]model.ActivityResult, 0, len(targets))
	for _, target := range targets {
		results = append(results, model.ActivityResult{
			Activity:            f.activities[target.NodeID],
			LatestReviewComment: target.LatestReviewComment,
			ETag:                target.ETag,
		})
	}
	return results, nil
}

func snapshotHasActivityAndReactions(
	snapshot model.Snapshot,
	activities map[string]*model.Activity,
) bool {
	if snapshot.RepositoryCount != 2 || len(snapshot.Items) != 2 {
		return false
	}
	for _, item := range snapshot.Items {
		expected := activities[item.NodeID]
		if expected == nil ||
			item.LatestActivity == nil ||
			*item.LatestActivity != *expected ||
			len(item.Reactions) != 1 {
			return false
		}
	}
	return true
}

type retryableError struct {
	message string
	retryAt time.Time
}

func (e retryableError) Error() string {
	return e.message
}

func (e retryableError) RetryAt() time.Time {
	return e.retryAt
}

func deferNonActivityResources(
	t *testing.T,
	database *store.Store,
	host string,
	now time.Time,
) {
	t.Helper()
	resources, err := database.ListDueResources(
		context.Background(),
		host,
		now.Add(time.Second),
		100,
	)
	if err != nil {
		t.Fatalf("ListDueResources() error = %v", err)
	}
	for _, resource := range resources {
		if resource.Kind == model.ResourceKindActivity {
			continue
		}
		resource.NextPollAt = now.Add(24 * time.Hour)
		if err := database.SavePollResource(context.Background(), resource); err != nil {
			t.Fatalf("SavePollResource() error = %v", err)
		}
	}
}
