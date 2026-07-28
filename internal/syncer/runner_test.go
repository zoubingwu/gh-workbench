package syncer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
	"github.com/zoubingwu/gh-workbench/internal/store"
)

func TestRunnerDiscoversRelevantPullRequestsAndPollsReactions(t *testing.T) {
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
	source := &fakeSource{
		items: model.ItemsResult{Items: items},
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
			if snapshot.RepositoryCount == 2 &&
				len(snapshot.Items) == 2 &&
				len(snapshot.Items[0].Reactions) == 1 &&
				len(snapshot.Items[1].Reactions) == 1 {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("Runner.Run() error = %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("Runner.Run() did not stop after cancellation")
				}
				return
			}
		case <-timeout.C:
			t.Fatal("runner did not publish a reaction snapshot")
		}
	}
}

type fakeSource struct {
	items     model.ItemsResult
	reactions map[string]model.ReactionsResult
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
