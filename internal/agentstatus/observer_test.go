package agentstatus

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestObserverRunPublishesResolvedActivity(t *testing.T) {
	t.Parallel()

	changed := make(chan struct{}, 1)
	observer := &Observer{
		sources: []source{staticSource{observations: []observation{{
			provider:   "codex",
			sessionKey: "thread-1",
			cwd:        "/workspace/rocket",
			state:      model.LocalAgentStateWorking,
			confidence: model.LocalAgentConfidenceHeuristic,
		}}}},
		resolve: func(context.Context, string) (gitIdentity, error) {
			return gitIdentity{
				repositoryKey: "github.com/octocat/rocket",
				branch:        "ship-rocket",
				sha:           "1111111111111111111111111111111111111111",
			}, nil
		},
		interval: time.Hour,
		onChange: func() {
			changed <- struct{}{}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- observer.Run(ctx)
	}()

	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("observer did not publish its initial activity")
	}
	items := []model.WorkItem{{
		Kind:              model.ItemKindPullRequest,
		HeadRepositoryKey: "github.com/octocat/rocket",
		HeadRefName:       "ship-rocket",
	}}
	observer.Decorate(items)
	if items[0].LocalAgentActivity == nil {
		t.Fatal("resolved activity is nil")
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("observer did not stop after cancellation")
	}
}

func TestObserverCachesGitIdentityAcrossScans(t *testing.T) {
	t.Parallel()

	resolutions := 0
	observer := &Observer{
		sources: []source{staticSource{observations: []observation{{
			provider:   "codex",
			sessionKey: "thread-1",
			cwd:        "/workspace/rocket",
			state:      model.LocalAgentStateWorking,
			confidence: model.LocalAgentConfidenceHeuristic,
		}}}},
		resolve: func(context.Context, string) (gitIdentity, error) {
			resolutions++
			return gitIdentity{
				repositoryKey: "github.com/octocat/rocket",
				branch:        "ship-rocket",
				sha:           "1111111111111111111111111111111111111111",
			}, nil
		},
	}
	now := time.Now().UTC()

	observer.refresh(context.Background(), now)
	observer.refresh(context.Background(), now.Add(3*time.Second))
	if resolutions != 1 {
		t.Fatalf("Git resolutions after cached scan = %d, want 1", resolutions)
	}

	observer.refresh(context.Background(), now.Add(gitIdentityCacheTTL))
	if resolutions != 2 {
		t.Fatalf("Git resolutions after cache expiry = %d, want 2", resolutions)
	}
}

func TestObserverDecoratesMatchingPullRequests(t *testing.T) {
	t.Parallel()

	observer := &Observer{
		activities: []resolvedObservation{
			{
				provider:   "codex",
				sessionKey: "codex-1",
				state:      model.LocalAgentStateWorking,
				confidence: model.LocalAgentConfidenceHeuristic,
				identity: gitIdentity{
					repositoryKey: "github.com/octocat/rocket",
					branch:        "ship-rocket",
					sha:           "1111111111111111111111111111111111111111",
				},
			},
			{
				provider:   "claude",
				sessionKey: "claude-1",
				state:      model.LocalAgentStateNeedsInput,
				confidence: model.LocalAgentConfidenceSupported,
				identity: gitIdentity{
					repositoryKey: "github.com/octocat/rocket",
					branch:        "ship-rocket",
					sha:           "1111111111111111111111111111111111111111",
				},
			},
			{
				provider:   "claude",
				sessionKey: "claude-2",
				state:      model.LocalAgentStateWorking,
				confidence: model.LocalAgentConfidenceSupported,
				identity: gitIdentity{
					repositoryKey: "github.com/acme/satellite",
					branch:        "renamed-locally",
					sha:           "2222222222222222222222222222222222222222",
				},
			},
		},
	}
	items := []model.WorkItem{
		{
			Kind:              model.ItemKindPullRequest,
			HeadRepositoryKey: "github.com/octocat/rocket",
			HeadRefName:       "ship-rocket",
			HeadRefOID:        "1111111111111111111111111111111111111111",
		},
		{
			Kind:              model.ItemKindPullRequest,
			HeadRepositoryKey: "github.com/acme/satellite",
			HeadRefName:       "online",
			HeadRefOID:        "2222222222222222222222222222222222222222",
		},
		{
			Kind:              model.ItemKindPullRequest,
			HeadRepositoryKey: "github.com/acme/unmatched",
			HeadRefName:       "other",
			HeadRefOID:        "3333333333333333333333333333333333333333",
		},
		{
			Kind:              model.ItemKindIssue,
			HeadRepositoryKey: "github.com/octocat/rocket",
			HeadRefName:       "ship-rocket",
		},
	}

	observer.Decorate(items)

	first := items[0].LocalAgentActivity
	if first == nil {
		t.Fatal("branch-matched pull request activity is nil")
	}
	if first.State != model.LocalAgentStateNeedsInput ||
		first.Confidence != model.LocalAgentConfidenceSupported ||
		first.SessionCount != 1 {
		t.Fatalf("branch-matched pull request activity = %#v", first)
	}
	if !slices.Equal(first.Providers, []string{"claude"}) {
		t.Fatalf("branch-matched providers = %q", first.Providers)
	}

	second := items[1].LocalAgentActivity
	if second == nil {
		t.Fatal("SHA-matched pull request activity is nil")
	}
	if second.State != model.LocalAgentStateWorking ||
		second.Confidence != model.LocalAgentConfidenceSupported ||
		second.SessionCount != 1 ||
		!slices.Equal(second.Providers, []string{"claude"}) {
		t.Fatalf("SHA-matched pull request activity = %#v", second)
	}

	if items[2].LocalAgentActivity != nil {
		t.Fatalf("unmatched pull request activity = %#v", items[2].LocalAgentActivity)
	}
	if items[3].LocalAgentActivity != nil {
		t.Fatalf("issue activity = %#v", items[3].LocalAgentActivity)
	}
}

func TestObserverMatchesForkCheckoutThroughBaseRemoteAndSHA(t *testing.T) {
	t.Parallel()

	observer := &Observer{
		activities: []resolvedObservation{{
			provider:   "codex",
			sessionKey: "codex-1",
			state:      model.LocalAgentStateWorking,
			confidence: model.LocalAgentConfidenceHeuristic,
			identity: gitIdentity{
				repositoryKey: "github.com/Upstream/Rocket",
				branch:        "pr-branch",
				sha:           "1111111111111111111111111111111111111111",
			},
		}},
	}
	items := []model.WorkItem{{
		RepositoryKey:     "github.com/upstream/rocket",
		Kind:              model.ItemKindPullRequest,
		HeadRepositoryKey: "github.com/octocat/rocket",
		HeadRefName:       "pr-branch",
		HeadRefOID:        "1111111111111111111111111111111111111111",
	}}

	observer.Decorate(items)

	if items[0].LocalAgentActivity == nil {
		t.Fatal("fork checkout activity is nil")
	}
}

type staticSource struct {
	observations []observation
	err          error
}

func (s staticSource) scan(context.Context, time.Time) ([]observation, error) {
	return slices.Clone(s.observations), s.err
}

func TestObserverDecorateClearsStaleActivity(t *testing.T) {
	t.Parallel()

	observer := &Observer{}
	items := []model.WorkItem{{
		Kind: model.ItemKindPullRequest,
		LocalAgentActivity: &model.LocalAgentActivity{
			State:        model.LocalAgentStateWorking,
			Providers:    []string{"codex"},
			SessionCount: 1,
			Confidence:   model.LocalAgentConfidenceHeuristic,
		},
	}}

	observer.Decorate(items)

	if items[0].LocalAgentActivity != nil {
		t.Fatalf("stale activity = %#v", items[0].LocalAgentActivity)
	}
}
