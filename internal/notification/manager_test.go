package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestManagerUsesInitialSnapshotAsSilentBaseline(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	snapshot := testSnapshot(testItem())

	if err := manager.Observe(t.Context(), snapshot); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages = %#v, want none", sender.messages)
	}
}

func TestActivityVerbIncludesCommit(t *testing.T) {
	t.Parallel()

	if got, want := activityVerb("commit"), "committed"; got != want {
		t.Fatalf("activityVerb(commit) = %q, want %q", got, want)
	}
}

func TestManagerSendsNewItemsAndActivity(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	initial := testSnapshot()
	if err := manager.Observe(t.Context(), initial); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}

	item := testItem()
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("new item Observe() error = %v", err)
	}
	expectedNewItem := Message{
		Title: "acme/web #42: Add system notifications",
		Body:  "New relevant pull request from alice",
	}
	if len(sender.messages) != 1 || sender.messages[0] != expectedNewItem {
		t.Fatalf("new item messages = %#v, want %#v", sender.messages, expectedNewItem)
	}

	activity := &model.Activity{
		Kind:       "review_approved",
		Actor:      "bob",
		BodyText:   "Looks good.",
		OccurredAt: initial.GeneratedAt.Add(time.Minute),
		URL:        item.URL + "#pullrequestreview-1",
	}
	item.LatestActivity = activity
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("activity Observe() error = %v", err)
	}
	expectedActivity := Message{
		Title: "acme/web #42: Add system notifications",
		Body:  "bob approved: Looks good.",
	}
	if len(sender.messages) != 2 || sender.messages[1] != expectedActivity {
		t.Fatalf("activity messages = %#v, want second %#v", sender.messages, expectedActivity)
	}
}

func TestManagerFiltersViewerActivityAndItemsByOtherAuthors(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	if err := manager.Observe(t.Context(), testSnapshot()); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}

	otherPullRequest := testItem()
	ownPullRequest := testItem()
	ownPullRequest.Number = 43
	ownPullRequest.URL = "https://github.com/acme/web/pull/43"
	ownPullRequest.Author = "OCTOCAT"
	otherIssue := testItem()
	otherIssue.Number = 44
	otherIssue.Kind = model.ItemKindIssue
	otherIssue.URL = "https://github.com/acme/web/issues/44"
	ownIssue := otherIssue
	ownIssue.Number = 45
	ownIssue.URL = "https://github.com/acme/web/issues/45"
	ownIssue.Author = "octocat"

	filtered := testSnapshot(otherPullRequest, ownPullRequest, otherIssue, ownIssue)
	filtered.Notifications.OnlyMine = true
	if err := manager.Observe(t.Context(), filtered); err != nil {
		t.Fatalf("new items Observe() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages = %#v, want no new-item notifications", sender.messages)
	}

	ownPullRequest.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "octocat",
		OccurredAt: time.Date(2026, time.July, 28, 12, 1, 0, 0, time.UTC),
		URL:        ownPullRequest.URL + "#issuecomment-1",
	}
	filtered = testSnapshot(otherPullRequest, ownPullRequest, otherIssue, ownIssue)
	filtered.Notifications.OnlyMine = true
	if err := manager.Observe(t.Context(), filtered); err != nil {
		t.Fatalf("own activity Observe() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages after own activity = %#v, want unchanged", sender.messages)
	}

	ownPullRequest.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "bob",
		OccurredAt: time.Date(2026, time.July, 28, 12, 2, 0, 0, time.UTC),
		URL:        ownPullRequest.URL + "#issuecomment-2",
	}
	filtered = testSnapshot(otherPullRequest, ownPullRequest, otherIssue, ownIssue)
	filtered.Notifications.OnlyMine = true
	if err := manager.Observe(t.Context(), filtered); err != nil {
		t.Fatalf("other activity Observe() error = %v", err)
	}
	if len(sender.messages) != 1 ||
		sender.messages[0].Body != "bob commented" {
		t.Fatalf(
			"sent messages after other activity = %#v, want own pull request activity",
			sender.messages,
		)
	}
}

func TestManagerAdvancesCursorWhileDisabledAndAcrossOmissions(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	initial := testSnapshot(testItem())
	if err := manager.Observe(t.Context(), initial); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}

	item := testItem()
	item.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "alice",
		OccurredAt: initial.GeneratedAt.Add(time.Minute),
		URL:        item.URL + "#issuecomment-1",
	}
	disabled := testSnapshot(item)
	disabled.Notifications.Enabled = false
	if err := manager.Observe(t.Context(), disabled); err != nil {
		t.Fatalf("disabled Observe() error = %v", err)
	}

	if err := manager.Observe(t.Context(), testSnapshot()); err != nil {
		t.Fatalf("omitted Observe() error = %v", err)
	}
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("restored Observe() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages = %#v, want none", sender.messages)
	}
}

func TestManagerSeedsRetainedItemsBeforeInitialSnapshot(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	item := testItem()
	manager.Seed([]model.WorkItem{item})

	if err := manager.Observe(t.Context(), testSnapshot()); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("restored Observe() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages = %#v, want none", sender.messages)
	}
}

func TestManagerSilencesCommitHydrationBeforeCachedUpdate(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	item := testItem()
	item.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "alice",
		OccurredAt: item.UpdatedAt.Add(-5 * time.Minute),
		URL:        item.URL + "#issuecomment-1",
	}
	manager.Seed([]model.WorkItem{item})

	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("baseline Observe() error = %v", err)
	}

	historicalCommit := &model.Activity{
		Kind:       "commit",
		Actor:      "alice",
		OccurredAt: item.UpdatedAt,
		URL:        item.URL + "/commits/historical",
	}
	item.LatestActivity = historicalCommit
	item.LatestCommit = historicalCommit
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("hydrated commit Observe() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("hydrated commit messages = %#v, want none", sender.messages)
	}

	newCommit := &model.Activity{
		Kind:       "commit",
		Actor:      "alice",
		OccurredAt: item.UpdatedAt.Add(time.Minute),
		URL:        item.URL + "/commits/new",
	}
	item.LatestActivity = newCommit
	item.LatestCommit = newCommit
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("new commit Observe() error = %v", err)
	}
	if len(sender.messages) != 1 || sender.messages[0].Body != "alice committed" {
		t.Fatalf("new commit messages = %#v, want one commit", sender.messages)
	}
}

func TestManagerReportsNewActivityAfterAQuietBaseline(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	item := testItem()
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}

	item.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "alice",
		OccurredAt: item.UpdatedAt.Add(time.Minute),
		URL:        item.URL + "#issuecomment-delayed",
	}
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("delayed activity Observe() error = %v", err)
	}
	if len(sender.messages) != 1 ||
		sender.messages[0].Body != "alice commented" {
		t.Fatalf(
			"sent messages = %#v, want delayed activity",
			sender.messages,
		)
	}
}

func TestManagerReportsDistinctActivityAtTheSameTime(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	item := testItem()
	occurredAt := item.UpdatedAt.Add(time.Minute)
	item.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "alice",
		OccurredAt: occurredAt,
		URL:        item.URL + "#issuecomment-1",
	}
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}

	item.LatestActivity = &model.Activity{
		Kind:       "review_comment",
		Actor:      "bob",
		OccurredAt: occurredAt,
		URL:        item.URL + "#discussion_r2",
	}
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("same-time activity Observe() error = %v", err)
	}
	if len(sender.messages) != 1 ||
		sender.messages[0].Body != "bob left a review comment" {
		t.Fatalf(
			"sent messages = %#v, want same-time review comment",
			sender.messages,
		)
	}

	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("repeated activity Observe() error = %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages after repeat = %#v, want unchanged", sender.messages)
	}
}

func TestManagerSuppressesFirstActivityHydrationForNewItem(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	if err := manager.Observe(t.Context(), testSnapshot()); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}

	item := testItem()
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("new item Observe() error = %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("new item messages = %#v, want one", sender.messages)
	}

	item.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "alice",
		OccurredAt: item.UpdatedAt,
		URL:        item.URL + "#issuecomment-existing",
	}
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("existing activity Observe() error = %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf(
			"messages after existing activity hydration = %#v, want unchanged",
			sender.messages,
		)
	}

	item.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "bob",
		OccurredAt: item.UpdatedAt.Add(time.Minute),
		URL:        item.URL + "#issuecomment-new",
	}
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("new activity Observe() error = %v", err)
	}
	if len(sender.messages) != 2 ||
		sender.messages[1].Body != "bob commented" {
		t.Fatalf(
			"messages after new activity = %#v, want new comment",
			sender.messages,
		)
	}
}

func TestManagerWaitsForInitialSuccessfulSyncBeforeBaseline(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	manager := New(sender)
	unsynchronized := testSnapshot()
	unsynchronized.Sync.LastSuccess = nil
	if err := manager.Observe(t.Context(), unsynchronized); err != nil {
		t.Fatalf("unsynchronized Observe() error = %v", err)
	}

	item := testItem()
	synchronized := testSnapshot(item)
	if err := manager.Observe(t.Context(), synchronized); err != nil {
		t.Fatalf("synchronized Observe() error = %v", err)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages = %#v, want silent synchronized baseline", sender.messages)
	}
}

func TestManagerReturnsSenderErrorsAfterAdvancingCursor(t *testing.T) {
	t.Parallel()

	sendErr := errors.New("desktop unavailable")
	sender := &fakeSender{err: sendErr}
	manager := New(sender)
	if err := manager.Observe(t.Context(), testSnapshot()); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}

	item := testItem()
	err := manager.Observe(t.Context(), testSnapshot(item))
	if !errors.Is(err, sendErr) {
		t.Fatalf("Observe() error = %v, want %v", err, sendErr)
	}

	sender.err = nil
	if err := manager.Observe(t.Context(), testSnapshot(item)); err != nil {
		t.Fatalf("retry Observe() error = %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("send attempts = %d, want 1", len(sender.messages))
	}
}

type fakeSender struct {
	messages []Message
	err      error
}

func (f *fakeSender) Send(_ context.Context, message Message) error {
	f.messages = append(f.messages, message)
	return f.err
}

func testSnapshot(items ...model.WorkItem) model.Snapshot {
	lastSuccess := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	return model.Snapshot{
		Viewer:      "octocat",
		GeneratedAt: time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		Sync: model.SyncStatus{
			LastSuccess: &lastSuccess,
		},
		Notifications: model.NotificationPreferences{
			Enabled: true,
		},
		Items: items,
	}
}

func testItem() model.WorkItem {
	return model.WorkItem{
		Repository: "acme/web",
		Number:     42,
		Kind:       model.ItemKindPullRequest,
		Title:      "Add system notifications",
		URL:        "https://github.com/acme/web/pull/42",
		Author:     "alice",
		UpdatedAt:  time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
	}
}
