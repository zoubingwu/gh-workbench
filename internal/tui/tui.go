package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/zoubingwu/gh-workbench/internal/model"
)

const (
	inactiveAfter        = 30 * 24 * time.Hour
	openedActionDuration = 2 * time.Second
)

type SnapshotSource interface {
	Snapshot(context.Context) (model.Snapshot, error)
}

type Options struct {
	Source                        SnapshotSource
	Updates                       <-chan struct{}
	Trigger                       func()
	UpdateNotificationPreferences func(
		context.Context,
		model.NotificationPreferencesUpdate,
	) error
	OpenURL func(string) error
	Input   io.Reader
	Output  io.Writer
}

func Run(ctx context.Context, options Options) error {
	if options.Source == nil {
		return fmt.Errorf("start terminal UI: snapshot source is required")
	}

	program := tea.NewProgram(
		newModel(ctx, options, time.Now),
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
		tea.WithoutSignalHandler(),
	)
	if _, err := program.Run(); err != nil {
		if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("run terminal UI: %w", err)
	}
	return nil
}

type itemFilter string

const (
	filterAll          itemFilter = "all"
	filterPullRequests itemFilter = "pull_request"
	filterIssues       itemFilter = "issue"
)

type terminalModel struct {
	ctx context.Context

	source                        SnapshotSource
	updates                       <-chan struct{}
	trigger                       func()
	updateNotificationPreferences func(
		context.Context,
		model.NotificationPreferencesUpdate,
	) error
	openURL func(string) error
	now     func() time.Time

	snapshot                      model.Snapshot
	loaded                        bool
	loadError                     error
	action                        string
	actionVersion                 uint64
	filter                        itemFilter
	onlyMine                      bool
	showInactive                  bool
	savingNotificationPreferences bool
	cursor                        int
	selectedKey                   string
	width                         int
	height                        int
}

type snapshotLoadedMsg struct {
	snapshot model.Snapshot
	err      error
}

type updateAvailableMsg struct {
	open bool
}

type openResultMsg struct {
	repository string
	number     int
	err        error
}

type notificationPreferencesUpdatedMsg struct {
	update model.NotificationPreferencesUpdate
	err    error
}

type actionExpiredMsg struct {
	version uint64
	action  string
}

func newModel(
	ctx context.Context,
	options Options,
	now func() time.Time,
) terminalModel {
	return terminalModel{
		ctx:                           ctx,
		source:                        options.Source,
		updates:                       options.Updates,
		trigger:                       options.Trigger,
		updateNotificationPreferences: options.UpdateNotificationPreferences,
		openURL:                       options.OpenURL,
		now:                           now,
		filter:                        filterAll,
		onlyMine:                      true,
		width:                         100,
		height:                        30,
	}
}

func (m terminalModel) Init() tea.Cmd {
	return m.loadSnapshot()
}

func (m terminalModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
	case tea.KeyPressMsg:
		return m.updateKey(message)
	case snapshotLoadedMsg:
		if message.err != nil {
			m.loadError = message.err
			return m, m.waitForUpdate()
		}
		m.snapshot = message.snapshot
		m.onlyMine = message.snapshot.Notifications.OnlyMine
		m.loaded = true
		m.loadError = nil
		m.reconcileSelection()
		return m, m.waitForUpdate()
	case updateAvailableMsg:
		if message.open {
			return m, m.loadSnapshot()
		}
	case openResultMsg:
		if message.err != nil {
			m.setAction("Open failed: " + message.err.Error())
		} else {
			m.setAction(fmt.Sprintf(
				"Opened %s #%d",
				message.repository,
				message.number,
			))
			return m, m.expireAction()
		}
	case actionExpiredMsg:
		if message.version == m.actionVersion &&
			message.action == m.action {
			m.action = ""
		}
	case notificationPreferencesUpdatedMsg:
		m.savingNotificationPreferences = false
		if message.err != nil {
			m.action = "Notification settings update failed: " +
				message.err.Error()
			return m, nil
		}
		if message.update.Enabled != nil {
			enabled := *message.update.Enabled
			m.snapshot.Notifications.Enabled = enabled
			if enabled {
				m.action = "System notifications enabled"
			} else {
				m.action = "System notifications disabled"
			}
		}
		if message.update.OnlyMine != nil {
			onlyMine := *message.update.OnlyMine
			m.snapshot.Notifications.OnlyMine = onlyMine
			m.onlyMine = onlyMine
			m.action = "Only mine preference updated"
			m.reconcileSelection()
		}
	}
	return m, nil
}

func (m terminalModel) updateKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "pgdown":
		m.moveSelection(m.pageSize())
	case "pgup":
		m.moveSelection(-m.pageSize())
	case "1":
		m.filter = filterAll
		m.reconcileSelection()
	case "2":
		m.filter = filterPullRequests
		m.reconcileSelection()
	case "3":
		m.filter = filterIssues
		m.reconcileSelection()
	case "m":
		onlyMine := !m.onlyMine
		if m.updateNotificationPreferences == nil {
			m.onlyMine = onlyMine
			m.reconcileSelection()
			return m, nil
		}
		if m.savingNotificationPreferences {
			return m, nil
		}
		update := model.NotificationPreferencesUpdate{
			OnlyMine: &onlyMine,
		}
		m.savingNotificationPreferences = true
		m.action = "Saving Only mine preference"
		return m, m.saveNotificationPreferences(update)
	case "i":
		m.showInactive = !m.showInactive
		m.reconcileSelection()
	case "n":
		if !m.loaded ||
			!m.snapshot.Notifications.Supported ||
			m.updateNotificationPreferences == nil {
			m.action = "System notifications are unavailable"
			return m, nil
		}
		if m.savingNotificationPreferences {
			return m, nil
		}
		enabled := !m.snapshot.Notifications.Enabled
		update := model.NotificationPreferencesUpdate{Enabled: &enabled}
		m.savingNotificationPreferences = true
		m.action = "Saving system notification settings"
		return m, m.saveNotificationPreferences(update)
	case "r":
		if m.trigger != nil {
			m.trigger()
			m.setAction("Sync requested")
			return m, nil
		}
		m.setAction("Sync is unavailable")
	case "enter", "o":
		item, ok := m.selectedItem()
		if !ok {
			m.setAction("No work item selected")
			return m, nil
		}
		if m.openURL == nil {
			m.setAction("Opening links is unavailable")
			return m, nil
		}
		m.setAction(fmt.Sprintf(
			"Opening %s #%d",
			item.Repository,
			item.Number,
		))
		return m, func() tea.Msg {
			return openResultMsg{
				repository: item.Repository,
				number:     item.Number,
				err:        m.openURL(item.URL),
			}
		}
	}
	return m, nil
}

func (m terminalModel) saveNotificationPreferences(
	update model.NotificationPreferencesUpdate,
) tea.Cmd {
	return func() tea.Msg {
		return notificationPreferencesUpdatedMsg{
			update: update,
			err: m.updateNotificationPreferences(
				m.ctx,
				update,
			),
		}
	}
}

func (m *terminalModel) setAction(action string) {
	m.action = action
	m.actionVersion++
}

func (m terminalModel) expireAction() tea.Cmd {
	version := m.actionVersion
	action := m.action
	return tea.Tick(openedActionDuration, func(time.Time) tea.Msg {
		return actionExpiredMsg{
			version: version,
			action:  action,
		}
	})
}

func (m terminalModel) loadSnapshot() tea.Cmd {
	if m.source == nil {
		return nil
	}
	return func() tea.Msg {
		snapshot, err := m.source.Snapshot(m.ctx)
		return snapshotLoadedMsg{snapshot: snapshot, err: err}
	}
}

func (m terminalModel) waitForUpdate() tea.Cmd {
	if m.updates == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case _, open := <-m.updates:
			return updateAvailableMsg{open: open}
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m *terminalModel) moveSelection(delta int) {
	items := m.visibleItems()
	if len(items) == 0 {
		m.cursor = 0
		m.selectedKey = ""
		return
	}
	m.cursor = min(max(m.cursor+delta, 0), len(items)-1)
	m.selectedKey = itemKey(items[m.cursor])
}

func (m *terminalModel) reconcileSelection() {
	items := m.visibleItems()
	if len(items) == 0 {
		m.cursor = 0
		m.selectedKey = ""
		return
	}
	for index := range items {
		if itemKey(items[index]) == m.selectedKey {
			m.cursor = index
			return
		}
	}
	m.cursor = min(max(m.cursor, 0), len(items)-1)
	m.selectedKey = itemKey(items[m.cursor])
}

func (m terminalModel) selectedItem() (model.WorkItem, bool) {
	items := m.visibleItems()
	if len(items) == 0 {
		return model.WorkItem{}, false
	}
	index := min(max(m.cursor, 0), len(items)-1)
	return items[index], true
}

type itemCounts struct {
	all          int
	pullRequests int
	issues       int
}

func (m terminalModel) counts() itemCounts {
	var counts itemCounts
	for _, item := range m.activityItems() {
		counts.all++
		switch item.Kind {
		case model.ItemKindPullRequest:
			counts.pullRequests++
		case model.ItemKindIssue:
			counts.issues++
		}
	}
	return counts
}

func (m terminalModel) visibleItems() []model.WorkItem {
	items := make([]model.WorkItem, 0, len(m.snapshot.Items))
	for _, item := range m.activityItems() {
		if m.filter != filterAll && string(item.Kind) != string(m.filter) {
			continue
		}
		items = append(items, item)
	}
	return groupByRepository(items)
}

func (m terminalModel) activityItems() []model.WorkItem {
	items := make([]model.WorkItem, 0, len(m.snapshot.Items))
	for _, item := range m.accountItems() {
		if !m.showInactive && m.inactive(item) {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (m terminalModel) accountItems() []model.WorkItem {
	items := make([]model.WorkItem, 0, len(m.snapshot.Items))
	for _, item := range m.snapshot.Items {
		if m.onlyMine &&
			!strings.EqualFold(item.Author, m.snapshot.Viewer) {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (m terminalModel) inactive(item model.WorkItem) bool {
	if item.UpdatedAt.IsZero() {
		return false
	}
	reference := m.snapshot.GeneratedAt
	if reference.IsZero() {
		reference = m.now()
	}
	return reference.Sub(item.UpdatedAt) > inactiveAfter
}

func groupByRepository(items []model.WorkItem) []model.WorkItem {
	groups := make(map[string][]model.WorkItem)
	order := make([]string, 0)
	for _, item := range items {
		if _, exists := groups[item.Repository]; !exists {
			order = append(order, item.Repository)
		}
		groups[item.Repository] = append(groups[item.Repository], item)
	}

	grouped := make([]model.WorkItem, 0, len(items))
	for _, repository := range order {
		grouped = append(grouped, groups[repository]...)
	}
	return grouped
}

func itemKey(item model.WorkItem) string {
	return fmt.Sprintf("%s:%s:%d", item.Repository, item.Kind, item.Number)
}

func (m terminalModel) pageSize() int {
	items := m.visibleItems()
	if len(items) == 0 {
		return 1
	}
	start, end := m.visibleItemRange(items, m.listHeight())
	return max(1, end-start)
}
