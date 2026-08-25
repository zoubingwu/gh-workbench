package app

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/zoubingwu/gh-workbench/internal/model"
	"github.com/zoubingwu/gh-workbench/internal/server"
	"github.com/zoubingwu/gh-workbench/internal/tui"
)

const (
	demoHost   = "github.example"
	demoViewer = "demo-user"
)

type demoBackend struct {
	mu            sync.RWMutex
	notifications model.NotificationPreferences
}

func runDemo(ctx context.Context, options Options) error {
	backend := newDemoBackend()
	if !options.Browser {
		launcher := browser.New("", io.Discard, io.Discard)
		return tui.Run(ctx, tui.Options{
			Source:                        demoTerminalSource{backend: backend},
			Trigger:                       backend.Trigger,
			UpdateNotificationPreferences: backend.UpdateNotificationPreferences,
			OpenURL:                       launcher.Browse,
			Input:                         options.Stdin,
			Output:                        options.Stdout,
		})
	}

	localServer, err := server.New(
		backend,
		backend,
		demoHost,
		demoViewer,
		true,
		nil,
		nil,
	)
	if err != nil {
		return err
	}
	return serveLocal(
		ctx,
		options,
		localServer,
		demoViewer+"@"+demoHost,
	)
}

func newDemoBackend() *demoBackend {
	return &demoBackend{
		notifications: model.NotificationPreferences{
			Supported: true,
			Enabled:   true,
			OnlyMine:  true,
		},
	}
}

type demoTerminalSource struct {
	backend *demoBackend
}

func (s demoTerminalSource) Snapshot(
	ctx context.Context,
) (model.Snapshot, error) {
	return s.backend.Snapshot(
		ctx,
		demoHost,
		false,
		time.Now().UTC(),
	)
}

func (b *demoBackend) Snapshot(
	_ context.Context,
	_ string,
	_ bool,
	now time.Time,
) (model.Snapshot, error) {
	b.mu.RLock()
	notifications := b.notifications
	b.mu.RUnlock()

	approved := newDemoItem(
		now,
		"workbench-demo/orbit-web",
		84,
		model.ItemKindPullRequest,
		"Polish keyboard navigation for the command palette",
		demoViewer,
		3*time.Minute,
	)
	approved.ReviewDecision = "APPROVED"
	approved.MergeState = "CLEAN"
	approved.Additions = 184
	approved.Deletions = 47
	approved.LatestActivity = newDemoActivity(
		approved,
		"review_approved",
		"demo-reviewer",
		"Navigation now works cleanly with screen readers.",
		"#pullrequestreview-84",
	)
	approved.Reactions = []model.Reaction{
		newDemoReaction(1, "+1", "demo-maintainer", approved.UpdatedAt),
		newDemoReaction(2, "+1", "demo-reviewer", approved.UpdatedAt),
		newDemoReaction(3, "rocket", "demo-maintainer", approved.UpdatedAt),
	}
	approved.LocalAgentActivity = &model.LocalAgentActivity{
		State:        model.LocalAgentStateWorking,
		Providers:    []string{"claude", "codex"},
		SessionCount: 2,
		Confidence:   model.LocalAgentConfidenceSupported,
	}

	changesRequested := newDemoItem(
		now,
		"workbench-demo/relay-api",
		37,
		model.ItemKindPullRequest,
		"Retry transient webhook deliveries with bounded backoff",
		demoViewer,
		14*time.Minute,
	)
	changesRequested.ReviewDecision = "CHANGES_REQUESTED"
	changesRequested.MergeState = "BLOCKED"
	changesRequested.Additions = 128
	changesRequested.Deletions = 22
	changesRequested.LatestActivity = newDemoActivity(
		changesRequested,
		"review_comment",
		"demo-reviewer",
		"Please add a test for the final retry attempt.",
		"#discussion_r37",
	)
	changesRequested.Reactions = []model.Reaction{
		newDemoReaction(
			4,
			"eyes",
			"demo-review-bot",
			changesRequested.UpdatedAt,
		),
	}

	workspaceIssue := newDemoItem(
		now,
		"workbench-demo/orbit-web",
		91,
		model.ItemKindIssue,
		"Remember the selected workspace between sessions",
		"demo-maintainer",
		28*time.Minute,
	)
	workspaceIssue.Labels = []model.Label{
		{Name: "enhancement", Color: "1f6feb"},
		{Name: "accessibility", Color: "8250df"},
	}
	workspaceIssue.LatestActivity = newDemoActivity(
		workspaceIssue,
		"comment",
		demoViewer,
		"Reproduced after switching between two workspaces.",
		"#issuecomment-91",
	)

	sessionGuide := newDemoItem(
		now,
		"workbench-demo/field-notes",
		18,
		model.ItemKindIssue,
		"Add a troubleshooting guide for browser sessions",
		"demo-writer",
		2*time.Hour,
	)
	sessionGuide.Labels = []model.Label{
		{Name: "documentation", Color: "0075ca"},
		{Name: "good first issue", Color: "7057ff"},
	}
	sessionGuide.LatestActivity = newDemoActivity(
		sessionGuide,
		"comment",
		"demo-maintainer",
		"The Brave example should include the complete session URL.",
		"#issuecomment-18",
	)

	syncHealth := newDemoItem(
		now,
		"workbench-demo/relay-api",
		42,
		model.ItemKindIssue,
		"Expose sync health in the status endpoint",
		demoViewer,
		6*time.Hour,
	)
	syncHealth.Labels = []model.Label{
		{Name: "enhancement", Color: "a2eeef"},
		{Name: "api", Color: "5319e7"},
	}
	syncHealth.LatestActivity = newDemoActivity(
		syncHealth,
		"labeled",
		"demo-reviewer",
		"api",
		"#event-42",
	)

	draft := newDemoItem(
		now,
		"workbench-demo/relay-api",
		29,
		model.ItemKindPullRequest,
		"Draft caching experiment for large repositories",
		demoViewer,
		2*24*time.Hour,
	)
	draft.IsDraft = true
	draft.ReviewDecision = "REVIEW_REQUIRED"
	draft.MergeState = "DRAFT"
	draft.Additions = 246
	draft.Deletions = 81
	draft.LatestActivity = newDemoActivity(
		draft,
		"commit",
		demoViewer,
		"7f3a91c Tighten cache bounds",
		"#commits-29",
	)

	inactive := newDemoItem(
		now,
		"workbench-demo/field-notes",
		7,
		model.ItemKindIssue,
		"Collect ideas for the next dashboard layout",
		"demo-writer",
		45*24*time.Hour,
	)
	inactive.Labels = []model.Label{
		{Name: "discussion", Color: "d4c5f9"},
	}
	inactive.LatestActivity = newDemoActivity(
		inactive,
		"comment",
		"demo-reviewer",
		"Keeping this thread for the next design pass.",
		"#issuecomment-7",
	)

	lastSuccess := now.Add(-20 * time.Second)
	return model.Snapshot{
		Host:            demoHost,
		Viewer:          demoViewer,
		RepositoryCount: 3,
		GeneratedAt:     now,
		Sync: model.SyncStatus{
			Running:     false,
			LastSuccess: &lastSuccess,
		},
		Notifications: notifications,
		Items: []model.WorkItem{
			approved,
			changesRequested,
			workspaceIssue,
			sessionGuide,
			syncHealth,
			draft,
			inactive,
		},
	}, nil
}

func (b *demoBackend) UpdateNotificationPreferences(
	_ context.Context,
	update model.NotificationPreferencesUpdate,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if update.Enabled != nil {
		b.notifications.Enabled = *update.Enabled
	}
	if update.OnlyMine != nil {
		b.notifications.OnlyMine = *update.OnlyMine
	}
	return nil
}

func (*demoBackend) Trigger() {}

func (*demoBackend) Running() bool {
	return false
}

func newDemoItem(
	now time.Time,
	repository string,
	number int,
	kind model.ItemKind,
	title string,
	author string,
	updatedAgo time.Duration,
) model.WorkItem {
	updatedAt := now.Add(-updatedAgo)
	lastPollAt := now.Add(-15 * time.Second)
	lastChangedAt := updatedAt
	itemPath := "issues"
	if kind == model.ItemKindPullRequest {
		itemPath = "pull"
	}

	return model.WorkItem{
		Repository: repository,
		Number:     number,
		Kind:       kind,
		Title:      title,
		URL: fmt.Sprintf(
			"https://%s/%s/%s/%d",
			demoHost,
			repository,
			itemPath,
			number,
		),
		State:       "OPEN",
		Author:      author,
		CreatedAt:   updatedAt.Add(-7 * 24 * time.Hour),
		UpdatedAt:   updatedAt,
		Labels:      make([]model.Label, 0),
		Reactions:   make([]model.Reaction, 0),
		MergeState:  "UNKNOWN",
		NeedsReview: false,
		Poll: model.PollStatus{
			IntervalSeconds: 60,
			NextPollAt:      now.Add(time.Minute),
			LastPollAt:      &lastPollAt,
			LastChangedAt:   &lastChangedAt,
			UnchangedCount:  0,
		},
	}
}

func newDemoActivity(
	item model.WorkItem,
	kind string,
	actor string,
	body string,
	fragment string,
) *model.Activity {
	return &model.Activity{
		Kind:       kind,
		Actor:      actor,
		BodyText:   body,
		OccurredAt: item.UpdatedAt,
		URL:        item.URL + fragment,
	}
}

func newDemoReaction(
	id int64,
	content string,
	user string,
	createdAt time.Time,
) model.Reaction {
	return model.Reaction{
		ID:        id,
		Content:   content,
		User:      user,
		CreatedAt: createdAt,
	}
}
