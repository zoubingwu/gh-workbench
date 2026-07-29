package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestModelUsesBrowserViewDefaults(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Host:            "github.com",
			Viewer:          "alice",
			RepositoryCount: 3,
			GeneratedAt:     now,
			Items: []model.WorkItem{
				workItem("acme/api", 3, model.ItemKindPullRequest, "alice", now),
				workItem("acme/docs", 8, model.ItemKindIssue, "bob", now),
				workItem("acme/api", 2, model.ItemKindPullRequest, "bob", now),
				workItem(
					"acme/old",
					1,
					model.ItemKindIssue,
					"alice",
					now.Add(-31*24*time.Hour),
				),
			},
		},
	})

	items := current.visibleItems()
	if got, want := len(items), 2; got != want {
		t.Fatalf("visible items = %d, want %d", got, want)
	}
	if items[0].Number != 3 || items[1].Number != 8 {
		t.Fatalf("visible item numbers = [%d %d], want [3 8]",
			items[0].Number,
			items[1].Number,
		)
	}
	counts := current.counts()
	if counts.all != 2 || counts.pullRequests != 1 || counts.issues != 1 {
		t.Fatalf("counts = %#v, want all=2 pullRequests=1 issues=1", counts)
	}

	view := current.View().Content
	for _, value := range []string{
		"GitHub Workbench",
		"alice@github.com",
		"acme/api",
		"acme/docs",
		"Only my PRs",
		"Show inactive",
	} {
		if !strings.Contains(view, value) {
			t.Fatalf("View() missing %q:\n%s", value, view)
		}
	}
	if strings.Contains(view, "acme/old") {
		t.Fatalf("View() contains inactive repository:\n%s", view)
	}
}

func TestModelFiltersAndTogglesItems(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items: []model.WorkItem{
				workItem("acme/api", 3, model.ItemKindPullRequest, "alice", now),
				workItem("acme/api", 2, model.ItemKindPullRequest, "bob", now),
				workItem(
					"acme/docs",
					8,
					model.ItemKindIssue,
					"bob",
					now.Add(-31*24*time.Hour),
				),
			},
		},
	})

	current = updateModel(t, current, keyPress("m"))
	if got, want := len(current.visibleItems()), 2; got != want {
		t.Fatalf("visible items after m = %d, want %d", got, want)
	}

	current = updateModel(t, current, keyPress("i"))
	if got, want := len(current.visibleItems()), 3; got != want {
		t.Fatalf("visible items after i = %d, want %d", got, want)
	}

	current = updateModel(t, current, keyPress("2"))
	items := current.visibleItems()
	if got, want := len(items), 2; got != want {
		t.Fatalf("pull requests = %d, want %d", got, want)
	}
	for _, item := range items {
		if item.Kind != model.ItemKindPullRequest {
			t.Fatalf("item kind = %q, want pull_request", item.Kind)
		}
	}
}

func TestModelKeepsSelectionAcrossSnapshots(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	first := workItem("acme/api", 1, model.ItemKindIssue, "alice", now)
	second := workItem("acme/api", 2, model.ItemKindIssue, "alice", now)
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items:       []model.WorkItem{first, second},
		},
	})
	current = updateModel(t, current, keyPress("j"))
	if selected, ok := current.selectedItem(); !ok || selected.Number != 2 {
		t.Fatalf("selected item = %#v, want number 2", selected)
	}

	third := workItem("acme/api", 3, model.ItemKindIssue, "alice", now)
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items:       []model.WorkItem{third, second, first},
		},
	})
	if selected, ok := current.selectedItem(); !ok || selected.Number != 2 {
		t.Fatalf("selected item after refresh = %#v, want number 2", selected)
	}
}

func TestModelPagesThroughItems(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	items := make([]model.WorkItem, 0, 10)
	for number := 1; number <= 10; number++ {
		items = append(items, workItem(
			"acme/api",
			number,
			model.ItemKindIssue,
			"alice",
			now,
		))
	}
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items:       items,
		},
	})
	current = updateModel(
		t,
		current,
		tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}),
	)

	item, ok := current.selectedItem()
	if !ok || item.Number != current.pageSize()+1 {
		t.Fatalf(
			"selected item = %#v, want number %d",
			item,
			current.pageSize()+1,
		)
	}
}

func TestModelReloadsSnapshotFromUpdateChannel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	source := &staticSource{
		snapshot: model.Snapshot{
			GeneratedAt: now,
			Items: []model.WorkItem{
				workItem("acme/api", 1, model.ItemKindIssue, "alice", now),
			},
		},
	}
	updates := make(chan struct{}, 1)
	current := newModel(context.Background(), Options{
		Source:  source,
		Updates: updates,
	}, func() time.Time {
		return now
	})

	message := current.Init()()
	updated, waitCommand := current.Update(message)
	current = updated.(terminalModel)
	if waitCommand == nil {
		t.Fatal("initial wait command = nil")
	}

	source.snapshot = model.Snapshot{
		GeneratedAt: now.Add(time.Second),
		Items: []model.WorkItem{
			workItem("acme/api", 2, model.ItemKindIssue, "alice", now),
		},
	}
	updates <- struct{}{}
	message = waitCommand()
	updated, loadCommand := current.Update(message)
	current = updated.(terminalModel)
	if loadCommand == nil {
		t.Fatal("reload command = nil")
	}
	message = loadCommand()
	updated, waitCommand = current.Update(message)
	current = updated.(terminalModel)
	if waitCommand == nil {
		t.Fatal("next wait command = nil")
	}

	item, ok := current.selectedItem()
	if !ok || item.Number != 2 {
		t.Fatalf("selected item = %#v, want refreshed item 2", item)
	}
	if view := current.View().Content; !strings.Contains(view, "#2") {
		t.Fatalf("View() missing refreshed item:\n%s", view)
	}
}

func TestModelShowsSyncAndSnapshotErrors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			GeneratedAt: now,
			Sync: model.SyncStatus{
				Running: true,
				Error:   "GitHub rate limit",
			},
		},
	})
	view := current.View().Content
	if !strings.Contains(view, "Syncing") ||
		!strings.Contains(view, "GitHub rate limit") {
		t.Fatalf("View() missing sync state or error:\n%s", view)
	}

	current = updateModel(t, current, snapshotLoadedMsg{
		err: errors.New("read cache"),
	})
	if view := current.View().Content; !strings.Contains(view, "read cache") {
		t.Fatalf("View() missing snapshot error:\n%s", view)
	}
}

func TestViewFitsTerminalWidth(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  42,
		Height: 18,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Host:            "github.com",
			Viewer:          "alice",
			RepositoryCount: 1,
			GeneratedAt:     now,
			Items: []model.WorkItem{
				{
					Repository: "acme/long-repository-name",
					Number:     1,
					Kind:       model.ItemKindPullRequest,
					Title:      strings.Repeat("long title ", 10),
					Author:     "alice",
					UpdatedAt:  now,
				},
			},
		},
	})

	view := current.View()
	if !view.AltScreen {
		t.Fatal("View().AltScreen = false, want true")
	}
	for _, line := range strings.Split(view.Content, "\n") {
		if width := ansi.StringWidth(line); width > 42 {
			t.Fatalf("line width = %d, want <= 42: %q", width, line)
		}
	}
}

func TestViewFillsTerminalHeight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	items := make([]model.WorkItem, 0, 10)
	for number := 1; number <= 10; number++ {
		items = append(items, workItem(
			"acme/repository-"+string(rune('a'+number)),
			number,
			model.ItemKindIssue,
			"alice",
			now,
		))
	}
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Sync: model.SyncStatus{
				Error: "sync error",
			},
			Items: items,
		},
	})
	current.action = "Sync requested"

	lines := strings.Split(current.View().Content, "\n")
	if len(lines) != 24 {
		t.Fatalf("view height = %d, want 24:\n%s", len(lines), current.View().Content)
	}
}

func TestViewPlacesTitleInShortcutFooterAndReclaimsTopLine(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	first := workItem(
		"acme/api",
		1,
		model.ItemKindIssue,
		"alice",
		now,
	)
	first.Title = "First work item"
	second := workItem(
		"acme/api",
		2,
		model.ItemKindIssue,
		"alice",
		now,
	)
	second.Title = "Second work item"

	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  120,
		Height: 11,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Host:            "github.com",
			Viewer:          "alice",
			RepositoryCount: 1,
			GeneratedAt:     now,
			Items: []model.WorkItem{
				first,
				second,
			},
		},
	})

	view := ansi.Strip(current.View().Content)
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], "alice@github.com") {
		t.Fatalf("first line = %q, want account header", lines[0])
	}
	footer := lines[len(lines)-1]
	for _, value := range []string{
		"GitHub Workbench",
		"↑/k ↓/j move",
	} {
		if !strings.Contains(footer, value) {
			t.Fatalf("footer missing %q: %q", value, footer)
		}
	}
	if count := strings.Count(view, "GitHub Workbench"); count != 1 {
		t.Fatalf("title count = %d, want 1:\n%s", count, view)
	}
	if !strings.Contains(view, "Second work item") {
		t.Fatalf("reclaimed top line did not expose second item:\n%s", view)
	}
}

func TestModelQuitKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message tea.KeyPressMsg
	}{
		{
			name:    "q",
			message: keyPress("q"),
		},
		{
			name: "control c",
			message: tea.KeyPressMsg(tea.Key{
				Code: 'c',
				Mod:  tea.ModCtrl,
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			current := newModel(context.Background(), Options{}, time.Now)
			_, command := current.Update(test.message)
			if command == nil {
				t.Fatal("quit command = nil")
			}
			message := command()
			if _, ok := message.(tea.QuitMsg); !ok {
				t.Fatalf("quit command message = %T, want tea.QuitMsg", message)
			}
		})
	}
}

func TestWaitForUpdateStopsWithContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	current := newModel(ctx, Options{
		Updates: make(chan struct{}),
	}, time.Now)
	cancel()

	command := current.waitForUpdate()
	if command == nil {
		t.Fatal("wait command = nil")
	}
	if message := command(); message != nil {
		t.Fatalf("wait command message = %#v, want nil", message)
	}
}

func TestRunReturnsAfterContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, Options{
		Source: staticSource{},
		Input:  strings.NewReader(""),
		Output: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunRestoresAlternateScreenOnQuitKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "q", input: "q"},
		{name: "control c", input: "\x03"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var output bytes.Buffer
			err := Run(ctx, Options{
				Source: staticSource{},
				Input:  strings.NewReader(test.input),
				Output: &output,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !strings.Contains(output.String(), ansi.SetModeAltScreenSaveCursor) {
				t.Fatalf("output missing alternate-screen entry: %q", output.String())
			}
			if !strings.Contains(output.String(), ansi.ResetModeAltScreenSaveCursor) {
				t.Fatalf("output missing alternate-screen restoration: %q", output.String())
			}
		})
	}
}

func TestRunRestoresAlternateScreenOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	input, inputWriter := io.Pipe()
	defer inputWriter.Close()
	output := &cancelOnAltScreenWriter{
		cancel: cancel,
	}
	err := Run(ctx, Options{
		Source: staticSource{},
		Input:  input,
		Output: output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), ansi.SetModeAltScreenSaveCursor) {
		t.Fatalf("output missing alternate-screen entry: %q", output.String())
	}
	if !strings.Contains(output.String(), ansi.ResetModeAltScreenSaveCursor) {
		t.Fatalf("output missing alternate-screen restoration: %q", output.String())
	}
}

func TestTerminalTextRemovesTerminalControlSequences(t *testing.T) {
	t.Parallel()

	input := "\x1b]52;c;Y29weQ==\x07safe\ntext\x00"
	if got, want := terminalText(input), "safe text"; got != want {
		t.Fatalf("terminalText() = %q, want %q", got, want)
	}
}

func TestSelectedItemUsesCompactBrowserRow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	item := workItem(
		"acme/api",
		7,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	item.Title = "Compact work item"
	item.ReviewDecision = "APPROVED"
	item.Additions = 10
	item.Deletions = 2
	item.LatestActivity = &model.Activity{
		Kind:       "comment",
		Actor:      "bob",
		BodyText:   "looks good",
		OccurredAt: now.Add(-15 * time.Minute),
	}
	item.Reactions = []model.Reaction{
		{Content: "eyes"},
	}
	item.Poll.Error = "rate limited"
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  160,
		Height: 24,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items:       []model.WorkItem{item},
		},
	})

	lines := strings.Split(ansi.Strip(current.View().Content), "\n")
	titleIndex := lineContaining(t, lines, "Compact work item")
	titleLine := lines[titleIndex]
	if !strings.Contains(
		titleLine,
		"⑂ Approved Compact work item  ·  +10 -2  ·  👀 1",
	) {
		t.Fatalf("compact title line has unexpected order: %q", titleLine)
	}
	for _, label := range []string{"Status:", "Changes:"} {
		if strings.Contains(titleLine, label) {
			t.Fatalf("compact title line contains %q: %q", label, titleLine)
		}
	}

	detailLine := lines[titleIndex+1]
	if !strings.Contains(detailLine, "#7 · opened by alice") {
		t.Fatalf("compact detail line has unexpected order: %q", detailLine)
	}
	for _, value := range []string{
		"bob commented 15m ago: looks good",
		"Poll error: rate limited",
	} {
		if !strings.Contains(detailLine, value) {
			t.Fatalf("compact detail line missing %q: %q", value, detailLine)
		}
	}
}

func TestViewShowsLocalAgentActivity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	item := workItem(
		"acme/api",
		7,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	item.LocalAgentActivity = &model.LocalAgentActivity{
		State:        model.LocalAgentStateWorking,
		Providers:    []string{"claude", "codex"},
		SessionCount: 2,
		Confidence:   model.LocalAgentConfidenceSupported,
	}
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  120,
		Height: 24,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items:       []model.WorkItem{item},
		},
	})

	view := ansi.Strip(current.View().Content)
	if !strings.Contains(view, "◌ Claude + Codex working") {
		t.Fatalf("View() missing local agent activity:\n%s", view)
	}
}

func TestViewKeepsStructuredDetailsVisibleForEveryItem(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	first := workItem(
		"acme/api",
		7,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	second := workItem(
		"acme/api",
		8,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	second.Title = "Second work item"
	second.ReviewDecision = "CHANGES_REQUESTED"
	second.Additions = 22
	second.Deletions = 3
	second.LatestActivity = &model.Activity{
		Kind:       "review_approved",
		Actor:      "carol",
		BodyText:   "ready to merge",
		OccurredAt: now.Add(-2 * time.Hour),
	}
	second.Reactions = []model.Reaction{
		{Content: "rocket"},
	}
	second.Poll.Error = "secondary poll failed"

	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  180,
		Height: 40,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items:       []model.WorkItem{first, second},
		},
	})

	lines := strings.Split(ansi.Strip(current.View().Content), "\n")
	firstTitleIndex := lineContaining(t, lines, first.Title)
	titleIndex := lineContaining(t, lines, "Second work item")
	if titleIndex+2 >= len(lines) {
		t.Fatalf("compact row ended outside rendered view:\n%s", strings.Join(lines, "\n"))
	}
	numberColumn := func(line, number string) int {
		t.Helper()
		index := strings.Index(line, number)
		if index < 0 {
			t.Fatalf("detail line missing %q: %q", number, line)
		}
		return ansi.StringWidth(line[:index])
	}
	if got, want := numberColumn(lines[titleIndex+1], "#8"),
		numberColumn(lines[firstTitleIndex+1], "#7"); got != want {
		t.Fatalf("PR number columns = [%d %d], want equal", want, got)
	}
	row := strings.Join(lines[titleIndex:titleIndex+2], "\n")
	for _, value := range []string{
		"Second work item",
		"Changes requested",
		"+22",
		"-3",
		"carol approved 2h ago: ready to merge",
		"🚀 1",
		"Poll error: secondary poll failed",
	} {
		if !strings.Contains(row, value) {
			t.Fatalf("compact row missing unselected item detail %q:\n%s", value, row)
		}
	}
	if nextLine := strings.TrimSpace(lines[titleIndex+2]); nextLine != "" {
		t.Fatalf("compact row uses more than two lines: %q", nextLine)
	}
}

func TestActivityVerbIncludesCommit(t *testing.T) {
	t.Parallel()

	if got, want := activityVerb("commit"), "committed"; got != want {
		t.Fatalf("activityVerb(commit) = %q, want %q", got, want)
	}
}

func TestViewColorsStatusesChangesAndLabels(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	approved := workItem(
		"acme/api",
		1,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	approved.ReviewDecision = "APPROVED"
	approved.Additions = 10
	approved.Deletions = 2

	changesRequested := workItem(
		"acme/api",
		2,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	changesRequested.ReviewDecision = "CHANGES_REQUESTED"

	reviewRequested := workItem(
		"acme/api",
		3,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	reviewRequested.NeedsReview = true

	draft := workItem(
		"acme/api",
		4,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	draft.IsDraft = true

	issue := workItem(
		"acme/api",
		5,
		model.ItemKindIssue,
		"alice",
		now,
	)
	issue.Title = "Labeled issue"
	issue.Labels = []model.Label{
		{Name: "bug", Color: "d73a4a"},
		{Name: "docs", Color: "fef2c0"},
	}
	unlabeledIssue := workItem(
		"acme/api",
		6,
		model.ItemKindIssue,
		"alice",
		now,
	)
	unlabeledIssue.Title = "Unlabeled issue"

	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  220,
		Height: 40,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items: []model.WorkItem{
				approved,
				changesRequested,
				reviewRequested,
				draft,
				issue,
				unlabeledIssue,
			},
		},
	})

	view := current.View().Content
	for _, value := range []string{
		ansiGreen + "Approved" + ansiReset,
		ansiRed + "Changes requested" + ansiReset,
		ansiYellow + "Review requested" + ansiReset,
		ansiDim + "Draft" + ansiReset,
		ansiGreen + "+10" + ansiReset,
		ansiRed + "-2" + ansiReset,
		"\x1b[97m\x1b[48;2;215;58;74m bug " + ansiReset,
		"\x1b[30m\x1b[48;2;254;242;192m docs " + ansiReset,
	} {
		if !strings.Contains(view, value) {
			t.Fatalf("View() missing color sequence %q:\n%s", value, view)
		}
	}

	lines := strings.Split(ansi.Strip(view), "\n")
	labeledLine := lines[lineContaining(t, lines, "Labeled issue")]
	if got := strings.Join(strings.Fields(labeledLine), " "); got !=
		"● Labeled issue · bug docs" {
		t.Fatalf("labeled issue line = %q", got)
	}
	unlabeledLine := lines[lineContaining(t, lines, "Unlabeled issue")]
	if got := strings.TrimSpace(unlabeledLine); got != "● Unlabeled issue" {
		t.Fatalf("unlabeled issue line = %q", got)
	}
}

func TestViewMovesTwoLineSelectionRailWithoutMaskingSemanticColors(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	approved := workItem(
		"acme/api",
		1,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	approved.Title = "Approved item"
	approved.ReviewDecision = "APPROVED"
	approved.Additions = 10
	approved.Deletions = 2

	changesRequested := workItem(
		"acme/api",
		2,
		model.ItemKindPullRequest,
		"alice",
		now,
	)
	changesRequested.Title = "Changes requested item"
	changesRequested.ReviewDecision = "CHANGES_REQUESTED"
	changesRequested.Additions = 5
	changesRequested.Deletions = 1

	current := newModel(context.Background(), Options{}, func() time.Time {
		return now
	})
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  180,
		Height: 24,
	})
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items: []model.WorkItem{
				approved,
				changesRequested,
			},
		},
	})

	assertSelectedRow := func(title string, colors []string) {
		t.Helper()

		view := current.View().Content
		lines := strings.Split(view, "\n")
		titleIndex := lineContaining(t, lines, title)
		row := strings.Join(lines[titleIndex:titleIndex+2], "\n")
		for index, line := range strings.Split(ansi.Strip(row), "\n") {
			if !strings.HasPrefix(line, "▌") {
				t.Fatalf(
					"selected row line %d missing selection rail: %q",
					index+1,
					line,
				)
			}
		}
		for _, color := range colors {
			if !strings.Contains(row, color) {
				t.Fatalf(
					"selected row missing semantic color %q:\n%s",
					color,
					row,
				)
			}
		}
		if strings.Contains(row, "\x1b[7m") {
			t.Fatalf("selected row uses reverse video:\n%s", row)
		}
	}

	assertSelectedRow("Approved item", []string{
		ansiGreen + "Approved" + ansiReset,
		ansiGreen + "+10" + ansiReset,
		ansiRed + "-2" + ansiReset,
	})

	current = updateModel(t, current, keyPress("j"))
	assertSelectedRow("Changes requested item", []string{
		ansiRed + "Changes requested" + ansiReset,
		ansiGreen + "+5" + ansiReset,
		ansiRed + "-1" + ansiReset,
	})

	lines := strings.Split(ansi.Strip(current.View().Content), "\n")
	firstIndex := lineContaining(t, lines, "Approved item")
	for _, line := range lines[firstIndex : firstIndex+2] {
		if strings.HasPrefix(line, "▌") {
			t.Fatalf("previous row retained selection rail: %q", line)
		}
	}
}

func TestModelTriggersSyncAndOpensSelectedItem(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var (
		triggered int
		openedURL string
	)
	current := newModel(
		context.Background(),
		Options{
			Source: staticSource{},
			Trigger: func() {
				triggered++
			},
			OpenURL: func(url string) error {
				openedURL = url
				return nil
			},
		},
		func() time.Time {
			return now
		},
	)
	item := workItem("acme/api", 7, model.ItemKindIssue, "alice", now)
	item.URL = "https://github.com/acme/api/issues/7"
	current = updateModel(t, current, snapshotLoadedMsg{
		snapshot: model.Snapshot{
			Viewer:      "alice",
			GeneratedAt: now,
			Items:       []model.WorkItem{item},
		},
	})

	updated, _ := current.Update(keyPress("r"))
	current = updated.(terminalModel)
	if triggered != 1 {
		t.Fatalf("trigger count = %d, want 1", triggered)
	}

	updated, command := current.Update(keyPress("enter"))
	current = updated.(terminalModel)
	if command == nil {
		t.Fatal("open command = nil")
	}
	message := command()
	current = updateModel(t, current, message)
	if openedURL != item.URL {
		t.Fatalf("opened URL = %q, want %q", openedURL, item.URL)
	}
	if !strings.Contains(current.action, "Opened") {
		t.Fatalf("action = %q, want open confirmation", current.action)
	}
}

type staticSource struct {
	snapshot model.Snapshot
	err      error
}

type cancelOnAltScreenWriter struct {
	mu     sync.Mutex
	once   sync.Once
	buffer bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnAltScreenWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	written, err := w.buffer.Write(value)
	enteredAltScreen := strings.Contains(
		w.buffer.String(),
		ansi.SetModeAltScreenSaveCursor,
	)
	w.mu.Unlock()
	if enteredAltScreen {
		w.once.Do(w.cancel)
	}
	return written, err
}

func (w *cancelOnAltScreenWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func (s staticSource) Snapshot(context.Context) (model.Snapshot, error) {
	return s.snapshot, s.err
}

func updateModel(t *testing.T, current terminalModel, message tea.Msg) terminalModel {
	t.Helper()

	updated, _ := current.Update(message)
	return updated.(terminalModel)
}

func keyPress(value string) tea.KeyPressMsg {
	if value == "enter" {
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	}
	runes := []rune(value)
	return tea.KeyPressMsg(tea.Key{
		Text: value,
		Code: runes[0],
	})
}

func lineContaining(t *testing.T, lines []string, value string) int {
	t.Helper()

	for index, line := range lines {
		if strings.Contains(line, value) {
			return index
		}
	}
	t.Fatalf("rendered lines missing %q:\n%s", value, strings.Join(lines, "\n"))
	return 0
}

func workItem(
	repository string,
	number int,
	kind model.ItemKind,
	author string,
	updatedAt time.Time,
) model.WorkItem {
	return model.WorkItem{
		Repository: repository,
		Number:     number,
		Kind:       kind,
		Title:      "Work item",
		URL:        "https://github.com/" + repository,
		Author:     author,
		UpdatedAt:  updatedAt,
	}
}
