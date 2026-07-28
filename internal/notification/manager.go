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
	cursors       map[string]activityCursor
	isInitialized bool
}

type activityCursor struct {
	occurredAt  time.Time
	identity    activityIdentity
	hasActivity bool
}

type activityIdentity struct {
	kind     string
	actor    string
	bodyText string
	url      string
}

func New(sender Sender) *Manager {
	return &Manager{
		sender:  sender,
		cursors: make(map[string]activityCursor),
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
				current,
				previous,
				exists,
			)
			if ok {
				messages = append(messages, message)
			}
		}

		if !exists || current.advances(previous) {
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
	current activityCursor,
	previous activityCursor,
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
		!current.isNewActivity(previous) ||
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
	previous activityCursor,
	exists bool,
) activityCursor {
	if item.LatestActivity != nil && !item.LatestActivity.OccurredAt.IsZero() {
		return activityCursor{
			occurredAt: item.LatestActivity.OccurredAt,
			identity: activityIdentity{
				kind:     item.LatestActivity.Kind,
				actor:    item.LatestActivity.Actor,
				bodyText: item.LatestActivity.BodyText,
				url:      item.LatestActivity.URL,
			},
			hasActivity: true,
		}
	}
	if !exists {
		return activityCursor{occurredAt: item.UpdatedAt}
	}
	return previous
}

func (c activityCursor) advances(previous activityCursor) bool {
	return c.occurredAt.After(previous.occurredAt) ||
		(c.occurredAt.Equal(previous.occurredAt) &&
			c.hasActivity &&
			c != previous)
}

func (c activityCursor) isNewActivity(previous activityCursor) bool {
	return c.occurredAt.After(previous.occurredAt) ||
		(c.occurredAt.Equal(previous.occurredAt) &&
			c.hasActivity &&
			previous.hasActivity &&
			c.identity != previous.identity)
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
