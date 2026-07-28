package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zoubingwu/gh-workbench/internal/model"
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiReverse = "\x1b[7m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiCyan    = "\x1b[36m"
)

func (m terminalModel) View() tea.View {
	var lines []string
	lines = append(lines, style("GitHub Workbench", ansiBold, ansiCyan))

	if m.loaded {
		lines = append(lines, m.headerLine())
	} else {
		lines = append(lines, style("Loading the initial snapshot…", ansiDim))
	}
	if message := m.errorLine(); message != "" {
		lines = append(lines, style("! "+message, ansiRed))
	}

	lines = append(lines, m.filterLine())
	lines = append(lines, style(strings.Repeat("─", max(m.width, 1)), ansiDim))

	items := m.visibleItems()
	if m.loaded && len(items) == 0 {
		lines = append(lines, "", style(m.emptyLine(), ansiDim))
	} else {
		lines = append(lines, m.itemLines(items)...)
	}

	if m.action != "" {
		lines = append(lines, "", style(terminalText(m.action), ansiCyan))
	}
	lines = append(lines, "", style(
		"↑/k ↓/j move  1–3 filter  m mine  i inactive  r sync  enter/o open  q quit",
		ansiDim,
	))

	for index := range lines {
		lines[index] = truncate(lines[index], m.width)
	}
	content := strings.Join(lines, "\n")
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "GitHub Workbench"
	return view
}

func (m terminalModel) headerLine() string {
	account := terminalText(m.snapshot.Viewer) +
		"@" +
		terminalText(m.snapshot.Host)
	if m.snapshot.Viewer == "" && m.snapshot.Host == "" {
		account = "active GitHub account"
	}

	state := style("Live", ansiGreen)
	if m.snapshot.Sync.Running {
		state = style("Syncing", ansiYellow)
	}
	lastSync := "waiting for first sync"
	if m.snapshot.Sync.LastSuccess != nil {
		lastSync = "synced " + relativeTime(*m.snapshot.Sync.LastSuccess, m.now())
	}
	return fmt.Sprintf(
		"%s  ·  %d repositories  ·  %s  ·  %s",
		account,
		m.snapshot.RepositoryCount,
		lastSync,
		state,
	)
}

func (m terminalModel) errorLine() string {
	if m.loadError != nil {
		return terminalText(m.loadError.Error())
	}
	return terminalText(m.snapshot.Sync.Error)
}

func (m terminalModel) filterLine() string {
	counts := m.counts()
	filters := []string{
		m.filterLabel(filterAll, "1 All", counts.all),
		m.filterLabel(
			filterPullRequests,
			"2 Pull requests",
			counts.pullRequests,
		),
		m.filterLabel(filterIssues, "3 Issues", counts.issues),
	}
	return strings.Join(filters, "  ") +
		"  │  " +
		checkbox(m.onlyMine) + " Only my PRs (m)  " +
		checkbox(m.showInactive) + " Show inactive (i)"
}

func (m terminalModel) filterLabel(
	filter itemFilter,
	label string,
	count int,
) string {
	value := fmt.Sprintf("%s %d", label, count)
	if m.filter == filter {
		return style("["+value+"]", ansiBold, ansiCyan)
	}
	return value
}

func (m terminalModel) itemLines(items []model.WorkItem) []string {
	if len(items) == 0 {
		return nil
	}

	pageSize := m.pageSize()
	start := m.cursor - pageSize/2
	start = min(max(start, 0), max(len(items)-pageSize, 0))
	end := min(start+pageSize, len(items))
	repositoryCounts := make(map[string]int)
	for _, item := range items {
		repositoryCounts[item.Repository]++
	}

	var lines []string
	previousRepository := ""
	for index := start; index < end; index++ {
		item := items[index]
		if item.Repository != previousRepository {
			lines = append(lines, "")
			lines = append(lines, style(fmt.Sprintf(
				"%s  %d %s",
				terminalText(item.Repository),
				repositoryCounts[item.Repository],
				plural(repositoryCounts[item.Repository], "item", "items"),
			), ansiBold))
			previousRepository = item.Repository
		}
		selected := index == m.cursor
		lines = append(lines, m.itemTitleLine(item, selected))
		lines = append(lines, m.itemDetailLine(item, selected))
		if selected {
			lines = append(lines, m.selectedDetailLines(item)...)
		}
	}

	if start > 0 || end < len(items) {
		lines = append(lines, style(fmt.Sprintf(
			"Showing %d–%d of %d items",
			start+1,
			end,
			len(items),
		), ansiDim))
	}
	return lines
}

func (m terminalModel) itemTitleLine(
	item model.WorkItem,
	selected bool,
) string {
	pointer := " "
	if selected {
		pointer = "›"
	}
	icon := "●"
	if item.Kind == model.ItemKindPullRequest {
		icon = "⑂"
	}

	line := fmt.Sprintf("%s %s %s", pointer, icon, terminalText(item.Title))
	if selected {
		return style(line, ansiReverse)
	}
	return line
}

func (m terminalModel) itemDetailLine(
	item model.WorkItem,
	selected bool,
) string {
	author := terminalText(item.Author)
	if author == "" {
		author = "ghost"
	}
	line := fmt.Sprintf(
		"    #%d · opened by %s · updated %s",
		item.Number,
		author,
		relativeTime(item.UpdatedAt, m.now()),
	)
	if selected {
		return style(line, ansiReverse)
	}
	return style(line, ansiDim)
}

func (m terminalModel) selectedDetailLines(item model.WorkItem) []string {
	summary := "      Labels: " + labelSummary(item.Labels)
	if item.Kind == model.ItemKindPullRequest {
		summary = fmt.Sprintf(
			"      Status: %s · Changes: %s+%d%s %s-%d%s",
			workItemStatus(item),
			ansiGreen,
			item.Additions,
			ansiReset,
			ansiRed,
			item.Deletions,
			ansiReset,
		)
	}

	activity := "none"
	if latest := item.LatestActivity; latest != nil {
		actor := terminalText(latest.Actor)
		if actor == "" {
			actor = "ghost"
		}
		activity = actor + " " + activityVerb(latest.Kind)
		if latest.BodyText != "" {
			activity += ": " + terminalText(latest.BodyText)
		}
	}

	reactions := reactionSummary(item.Reactions)
	if reactions == "" {
		reactions = "none"
	}
	polling := "healthy"
	if item.Poll.Error != "" {
		polling = terminalText(item.Poll.Error)
	}
	return []string{
		style(summary, ansiDim),
		style("      Activity: "+activity, ansiDim),
		style("      Reactions: "+reactions, ansiDim),
		style("      Polling: "+polling, ansiDim),
	}
}

func labelSummary(labels []model.Label) string {
	if len(labels) == 0 {
		return "none"
	}
	values := make([]string, 0, len(labels))
	for _, label := range labels {
		values = append(values, terminalText(label.Name))
	}
	return strings.Join(values, ", ")
}

func (m terminalModel) emptyLine() string {
	label := "work items"
	switch m.filter {
	case filterPullRequests:
		label = "pull requests"
	case filterIssues:
		label = "issues"
	}

	hidden := 0
	if !m.showInactive {
		for _, item := range m.accountItems() {
			if m.filter != filterAll && string(item.Kind) != string(m.filter) {
				continue
			}
			if m.inactive(item) {
				hidden++
			}
		}
	}
	if hidden > 0 {
		return fmt.Sprintf(
			"✓ No %s to show · %d inactive %s hidden",
			label,
			hidden,
			plural(hidden, "item", "items"),
		)
	}
	return "✓ No " + label + " to show · local cache is current"
}

func workItemStatus(item model.WorkItem) string {
	if item.IsDraft {
		return "Draft"
	}
	if item.NeedsReview {
		return "Review requested"
	}
	switch strings.ToUpper(item.ReviewDecision) {
	case "CHANGES_REQUESTED":
		return "Changes requested"
	case "APPROVED":
		return "Approved"
	case "REVIEW_REQUIRED":
		return "Review required"
	default:
		return "Open"
	}
}

func activityVerb(kind string) string {
	switch kind {
	case "comment":
		return "commented"
	case "review_comment":
		return "left a review comment"
	case "review_approved":
		return "approved"
	case "review_changes_requested":
		return "requested changes"
	case "review_commented":
		return "reviewed"
	case "review_dismissed":
		return "dismissed a review"
	case "labeled":
		return "labeled"
	case "unlabeled":
		return "removed label"
	case "reopened":
		return "reopened"
	case "review_requested":
		return "requested review"
	case "review_request_removed":
		return "removed review request"
	case "ready_for_review":
		return "marked ready for review"
	case "converted_to_draft":
		return "converted to draft"
	default:
		return "updated"
	}
}

func reactionSummary(reactions []model.Reaction) string {
	counts := make(map[string]int)
	order := make([]string, 0)
	for _, reaction := range reactions {
		if _, exists := counts[reaction.Content]; !exists {
			order = append(order, reaction.Content)
		}
		counts[reaction.Content]++
	}
	parts := make([]string, 0, len(order))
	for _, content := range order {
		parts = append(
			parts,
			reactionSymbol(content)+" "+strconv.Itoa(counts[content]),
		)
	}
	return strings.Join(parts, " ")
}

func reactionSymbol(content string) string {
	switch content {
	case "+1":
		return "👍"
	case "-1":
		return "👎"
	case "confused":
		return "😕"
	case "eyes":
		return "👀"
	case "heart":
		return "♥"
	case "hooray":
		return "🎉"
	case "laugh":
		return "😄"
	case "rocket":
		return "🚀"
	default:
		return terminalText(content)
	}
}

func relativeTime(value, now time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	elapsed := now.Sub(value)
	future := elapsed < 0
	if future {
		elapsed = -elapsed
	}

	var result string
	switch {
	case elapsed < time.Minute:
		result = "now"
	case elapsed < time.Hour:
		result = strconv.Itoa(int(elapsed/time.Minute)) + "m"
	case elapsed < 24*time.Hour:
		result = strconv.Itoa(int(elapsed/time.Hour)) + "h"
	default:
		result = strconv.Itoa(int(elapsed/(24*time.Hour))) + "d"
	}
	if result == "now" {
		return result
	}
	if future {
		return "in " + result
	}
	return result + " ago"
}

func terminalText(value string) string {
	value = ansi.Strip(value)
	value = strings.Map(func(character rune) rune {
		if !unicode.IsControl(character) {
			return character
		}
		if unicode.IsSpace(character) {
			return ' '
		}
		return -1
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func checkbox(checked bool) string {
	if checked {
		return "[x]"
	}
	return "[ ]"
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func style(value string, codes ...string) string {
	return strings.Join(codes, "") + value + ansiReset
}

func truncate(value string, width int) string {
	if width < 1 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}
