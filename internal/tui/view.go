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
	ansiReset       = "\x1b[0m"
	ansiBold        = "\x1b[1m"
	ansiDim         = "\x1b[2m"
	ansiBlack       = "\x1b[30m"
	ansiRed         = "\x1b[31m"
	ansiGreen       = "\x1b[32m"
	ansiYellow      = "\x1b[33m"
	ansiCyan        = "\x1b[36m"
	ansiBrightWhite = "\x1b[97m"
)

func (m terminalModel) View() tea.View {
	var lines []string

	if m.loaded {
		lines = append(lines, m.headerLine())
	} else {
		lines = append(lines, style("Loading the initial snapshot…", ansiDim))
	}
	if message := m.errorLine(); message != "" {
		lines = append(lines, style("! "+message, ansiRed))
	}

	lines = append(lines, m.controlLines()...)
	lines = append(lines, style(strings.Repeat("─", max(m.width, 1)), ansiDim))

	tail := make([]string, 0, 4)
	if m.action != "" {
		tail = append(
			tail,
			"",
			style(terminalText(m.action), ansiCyan),
		)
	}
	shortcuts := style(
		"↑/k ↓/j move  1–3 filter  m mine  i inactive  n notifications  r sync  enter/o open  q quit",
		ansiDim,
	)
	footer := style("GitHub Workbench", ansiBold, ansiCyan) +
		"  ·  " +
		shortcuts
	tail = append(tail, "", footer)

	items := m.visibleItems()
	if m.loaded && len(items) == 0 {
		lines = append(lines, "", style(m.emptyLine(), ansiDim))
	} else {
		lines = append(lines, m.itemLines(items, m.listHeight())...)
	}

	for len(lines)+len(tail) < m.height {
		lines = append(lines, "")
	}
	lines = append(lines, tail...)
	if m.height > 0 && len(lines) > m.height {
		footer := lines[len(lines)-1]
		lines = append(lines[:m.height-1], footer)
	}

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
	return strings.Join(filters, "  ")
}

func (m terminalModel) controlLines() []string {
	filterLine := m.filterLine()
	optionLine := m.notificationCheckbox() + "  " +
		checkbox(m.onlyMine) + " Only my PRs (m)  " +
		checkbox(m.showInactive) + " Show inactive (i)"
	combined := filterLine + "  │  " + optionLine
	if ansi.StringWidth(combined) <= m.width {
		return []string{combined}
	}
	return []string{filterLine, optionLine}
}

func (m terminalModel) notificationCheckbox() string {
	if !m.loaded || !m.snapshot.Notifications.Supported {
		return "[ ] Notifications unavailable"
	}
	return checkbox(m.snapshot.Notifications.Enabled) +
		" System notifications (n)"
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

func (m terminalModel) itemLines(
	items []model.WorkItem,
	lineBudget int,
) []string {
	if len(items) == 0 || lineBudget < 1 {
		return nil
	}

	start, end := m.visibleItemRange(items, lineBudget)
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
	}

	if start > 0 || end < len(items) {
		if len(lines) < lineBudget {
			lines = append(lines, style(fmt.Sprintf(
				"Showing %d–%d of %d items",
				start+1,
				end,
				len(items),
			), ansiDim))
		}
	}
	return lines
}

func (m terminalModel) visibleItemRange(
	items []model.WorkItem,
	lineBudget int,
) (int, int) {
	cursor := min(max(m.cursor, 0), len(items)-1)
	start, end := cursor, cursor+1
	for start > 0 &&
		itemRangeLineCount(items, start-1, end) <= lineBudget {
		start--
	}
	for end < len(items) &&
		itemRangeLineCount(items, start, end+1) <= lineBudget {
		end++
	}
	return start, end
}

func itemRangeLineCount(
	items []model.WorkItem,
	start int,
	end int,
) int {
	const itemLineCount = 2

	lines := (end - start) * itemLineCount
	previousRepository := ""
	for index := start; index < end; index++ {
		if items[index].Repository == previousRepository {
			continue
		}
		lines += 2
		previousRepository = items[index].Repository
	}
	return lines
}

func (m terminalModel) listHeight() int {
	fixedLines := 4 + len(m.controlLines())
	if m.errorLine() != "" {
		fixedLines++
	}
	if m.action != "" {
		fixedLines += 2
	}
	return max(m.height-fixedLines, 1)
}

func (m terminalModel) itemTitleLine(
	item model.WorkItem,
	selected bool,
) string {
	icon := "●"
	if item.Kind == model.ItemKindPullRequest {
		icon = "⑂"
	}
	title := terminalText(item.Title)
	if selected {
		title = style(title, ansiBold)
	}

	line := selectionRail(selected) + " " + icon + " "
	if item.Kind == model.ItemKindPullRequest {
		status := workItemStatus(item)
		line += style(status, statusColor(status)) + " "
	}
	line += title
	summary := itemSummary(item)
	if reactions := reactionSummary(item.Reactions); reactions != "" {
		if summary != "" {
			summary += "  ·  "
		}
		summary += reactions
	}
	line = joinItemSummary(
		line,
		summary,
		m.width,
	)
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
	parts := []string{
		fmt.Sprintf("#%d", item.Number),
		"opened by " + author,
		"updated " + relativeTime(item.UpdatedAt, m.now()),
	}
	if latest := item.LatestActivity; latest != nil {
		actor := terminalText(latest.Actor)
		if actor == "" {
			actor = "ghost"
		}
		activity := actor + " " + activityVerb(latest.Kind) + " " +
			relativeTime(latest.OccurredAt, m.now())
		if latest.BodyText != "" {
			activity += ": " + terminalText(latest.BodyText)
		}
		parts = append(parts, activity)
	}
	if item.Poll.Error != "" {
		parts = append(
			parts,
			"Poll error: "+terminalText(item.Poll.Error),
		)
	}
	line := strings.Join(parts, " · ")
	return selectionRail(selected) + "   " + style(line, ansiDim)
}

func selectionRail(selected bool) string {
	if selected {
		return style("▌", ansiCyan)
	}
	return " "
}

func itemSummary(item model.WorkItem) string {
	if item.Kind == model.ItemKindPullRequest {
		changes := style(
			"+"+strconv.Itoa(item.Additions),
			ansiGreen,
		) +
			" " +
			style(
				"-"+strconv.Itoa(item.Deletions),
				ansiRed,
			)
		if activity := localAgentSummary(item.LocalAgentActivity); activity != "" {
			return activity + "  " + changes
		}
		return changes
	}
	return labelSummary(item.Labels)
}

func localAgentSummary(activity *model.LocalAgentActivity) string {
	if activity == nil {
		return ""
	}

	providers := make([]string, 0, len(activity.Providers))
	for _, provider := range activity.Providers {
		if provider == "" {
			continue
		}
		providers = append(
			providers,
			strings.ToUpper(provider[:1])+provider[1:],
		)
	}
	if len(providers) == 0 {
		providers = append(providers, "Local agent")
	}

	indicator := "●"
	state := "working"
	color := ansiCyan
	if activity.State == model.LocalAgentStateNeedsInput {
		state = "needs input"
		color = ansiYellow
	} else if activity.Confidence == model.LocalAgentConfidenceSupported {
		indicator = "◌"
	}
	return style(
		indicator+" "+terminalText(strings.Join(providers, " + "))+" "+state,
		color,
	)
}

func joinItemSummary(prefix, summary string, width int) string {
	if summary == "" {
		return prefix
	}
	separator := "  ·  "
	suffix := separator + summary
	prefixWidth := width - ansi.StringWidth(suffix)
	if prefixWidth <= 1 {
		leading := ansi.Cut(prefix, 0, min(max(width, 0), 1))
		return leading + truncateLeft(
			suffix,
			width-ansi.StringWidth(leading),
		)
	}
	return truncate(prefix, prefixWidth) + suffix
}

func labelSummary(labels []model.Label) string {
	if len(labels) == 0 {
		return ""
	}
	values := make([]string, 0, len(labels))
	for _, label := range labels {
		values = append(values, labelText(label))
	}
	return strings.Join(values, " ")
}

func statusColor(status string) string {
	switch status {
	case "Open", "Approved":
		return ansiGreen
	case "Review requested", "Review required":
		return ansiYellow
	case "Changes requested":
		return ansiRed
	case "Draft":
		return ansiDim
	default:
		return ansiCyan
	}
}

func labelText(label model.Label) string {
	color := strings.TrimPrefix(strings.TrimSpace(label.Color), "#")
	value, err := strconv.ParseUint(color, 16, 24)
	if err != nil || len(color) != 6 {
		return terminalText(label.Name)
	}

	red := int(value >> 16)
	green := int(value >> 8 & 0xff)
	blue := int(value & 0xff)
	foreground := ansiBrightWhite
	luminance := (299*red + 587*green + 114*blue) / 1000
	if luminance > 160 {
		foreground = ansiBlack
	}

	background := fmt.Sprintf(
		"\x1b[48;2;%d;%d;%dm",
		red,
		green,
		blue,
	)
	return style(
		" "+terminalText(label.Name)+" ",
		foreground,
		background,
	)
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
	case "commit":
		return "committed"
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

func truncateLeft(value string, width int) string {
	if width < 1 {
		return ""
	}
	valueWidth := ansi.StringWidth(value)
	if valueWidth <= width {
		return value
	}

	const prefix = "…"
	if width <= ansi.StringWidth(prefix) {
		return prefix
	}
	cutWidth := valueWidth - width + ansi.StringWidth(prefix)
	for {
		truncated := ansi.TruncateLeft(value, cutWidth, prefix)
		if ansi.StringWidth(truncated) <= width {
			return truncated
		}
		cutWidth++
	}
}
