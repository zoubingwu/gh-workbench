package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

const sendTimeout = 5 * time.Second

var activityVerbs = map[string]string{
	"comment":                  "commented",
	"review_comment":           "left a review comment",
	"review_approved":          "approved",
	"review_changes_requested": "requested changes",
	"review_commented":         "reviewed",
	"review_dismissed":         "dismissed a review",
	"labeled":                  "labeled",
	"unlabeled":                "removed label",
	"reopened":                 "reopened",
	"review_requested":         "requested review",
	"review_request_removed":   "removed review request",
	"ready_for_review":         "marked ready for review",
	"converted_to_draft":       "converted to draft",
}

type Message struct {
	Title string
	Body  string
}

type Sender interface {
	Send(context.Context, Message) error
}

type Manager struct {
	sender        Sender
	cursors       map[string]time.Time
	isInitialized bool
}

func New(sender Sender) *Manager {
	return &Manager{
		sender:  sender,
		cursors: make(map[string]time.Time),
	}
}

func (m *Manager) Observe(ctx context.Context, snapshot model.Snapshot) error {
	messages := make([]Message, 0)
	for _, item := range snapshot.Items {
		key := itemKey(item)
		previous, exists := m.cursors[key]
		current := nextActivityCursor(item, previous, exists)

		if m.isInitialized && snapshot.Notifications.Enabled {
			message, ok := messageForChange(
				snapshot,
				item,
				previous,
				exists,
			)
			if ok {
				messages = append(messages, message)
			}
		}

		if !exists || current.After(previous) {
			m.cursors[key] = current
		}
	}
	if snapshot.Sync.LastSuccess != nil {
		m.isInitialized = true
	}

	var sendErrors []error
	for _, message := range messages {
		sendContext, cancel := context.WithTimeout(ctx, sendTimeout)
		err := m.sender.Send(sendContext, message)
		cancel()
		if err != nil {
			sendErrors = append(
				sendErrors,
				fmt.Errorf("send system notification: %w", err),
			)
		}
	}
	return errors.Join(sendErrors...)
}

func messageForChange(
	snapshot model.Snapshot,
	item model.WorkItem,
	previous time.Time,
	exists bool,
) (Message, bool) {
	isViewerItem := sameLogin(item.Author, snapshot.Viewer)
	isFilteredPullRequest := item.Kind == model.ItemKindPullRequest &&
		snapshot.Notifications.OnlyMyPullRequests &&
		!isViewerItem
	if isFilteredPullRequest {
		return Message{}, false
	}

	title := fmt.Sprintf(
		"%s #%d: %s",
		item.Repository,
		item.Number,
		item.Title,
	)
	if !exists {
		if isViewerItem {
			return Message{}, false
		}
		kind := "issue"
		if item.Kind == model.ItemKindPullRequest {
			kind = "pull request"
		}
		return Message{
			Title: title,
			Body: fmt.Sprintf(
				"New relevant %s from %s",
				kind,
				loginOrGhost(item.Author),
			),
		}, true
	}

	activity := item.LatestActivity
	if activity == nil ||
		!activity.OccurredAt.After(previous) ||
		sameLogin(activity.Actor, snapshot.Viewer) {
		return Message{}, false
	}
	body := loginOrGhost(activity.Actor) + " " + activityVerb(activity.Kind)
	if activity.BodyText != "" {
		body += ": " + activity.BodyText
	}
	return Message{
		Title: title,
		Body:  body,
	}, true
}

func itemKey(item model.WorkItem) string {
	return fmt.Sprintf("%s:%s:%d", item.Repository, item.Kind, item.Number)
}

func nextActivityCursor(
	item model.WorkItem,
	previous time.Time,
	exists bool,
) time.Time {
	if item.LatestActivity != nil && !item.LatestActivity.OccurredAt.IsZero() {
		return item.LatestActivity.OccurredAt
	}
	if !exists {
		return item.UpdatedAt
	}
	return previous
}

func sameLogin(login string, viewer string) bool {
	return strings.EqualFold(login, viewer)
}

func loginOrGhost(login string) string {
	if login == "" {
		return "ghost"
	}
	return login
}

func activityVerb(kind string) string {
	if verb, ok := activityVerbs[kind]; ok {
		return verb
	}
	return "updated"
}
