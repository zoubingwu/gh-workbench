package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ItemKind string

const (
	ItemKindIssue       ItemKind = "issue"
	ItemKindPullRequest ItemKind = "pull_request"
)

type ResourceKind string

const (
	ResourceKindWorkItems ResourceKind = "work_items"
	ResourceKindActivity  ResourceKind = "activity"
	ResourceKindReactions ResourceKind = "reactions"
)

type Repository struct {
	Host      string
	Owner     string
	Name      string
	UpdatedAt time.Time
}

func (r Repository) FullName() string {
	return r.Owner + "/" + r.Name
}

func (r Repository) Key() string {
	return r.Host + "/" + r.FullName()
}

type Reaction struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	User      string    `json:"user"`
	CreatedAt time.Time `json:"createdAt"`
}

type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Activity struct {
	Kind       string    `json:"kind"`
	Actor      string    `json:"actor"`
	BodyText   string    `json:"bodyText"`
	OccurredAt time.Time `json:"occurredAt"`
	URL        string    `json:"url"`
}

type PollStatus struct {
	IntervalSeconds int64      `json:"intervalSeconds"`
	NextPollAt      time.Time  `json:"nextPollAt"`
	LastPollAt      *time.Time `json:"lastPollAt"`
	LastChangedAt   *time.Time `json:"lastChangedAt"`
	UnchangedCount  int        `json:"unchangedCount"`
	Error           string     `json:"error,omitempty"`
}

type WorkItem struct {
	NodeID              string     `json:"-"`
	Repository          string     `json:"repository"`
	RepositoryKey       string     `json:"-"`
	Number              int        `json:"number"`
	Kind                ItemKind   `json:"kind"`
	Title               string     `json:"title"`
	URL                 string     `json:"url"`
	State               string     `json:"state"`
	Author              string     `json:"author"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	IsDraft             bool       `json:"isDraft"`
	ReviewDecision      string     `json:"reviewDecision"`
	MergeState          string     `json:"mergeState"`
	NeedsReview         bool       `json:"needsReview"`
	Additions           int        `json:"additions"`
	Deletions           int        `json:"deletions"`
	Labels              []Label    `json:"labels"`
	LatestActivity      *Activity  `json:"latestActivity"`
	LatestReviewComment *Activity  `json:"-"`
	Reactions           []Reaction `json:"reactions"`
	Poll                PollStatus `json:"poll"`
}

type ItemsResult struct {
	Items     []WorkItem
	ETag      string
	Unchanged bool
}

type ReactionsResult struct {
	Reactions []Reaction
	ETag      string
	Unchanged bool
}

type ActivityTarget struct {
	NodeID              string
	Repository          Repository
	Number              int
	Kind                ItemKind
	LatestReviewComment *Activity
	ETag                string
}

type ActivityResult struct {
	Activity            *Activity
	LatestReviewComment *Activity
	ETag                string
}

type SyncStatus struct {
	Running     bool       `json:"running"`
	LastSuccess *time.Time `json:"lastSuccess"`
	Error       string     `json:"error,omitempty"`
}

type NotificationPreferences struct {
	Supported          bool `json:"supported"`
	Enabled            bool `json:"enabled"`
	OnlyMyPullRequests bool `json:"onlyMyPullRequests"`
}

type Snapshot struct {
	Host            string                  `json:"host"`
	Viewer          string                  `json:"viewer"`
	RepositoryCount int                     `json:"repositoryCount"`
	GeneratedAt     time.Time               `json:"generatedAt"`
	Sync            SyncStatus              `json:"sync"`
	Notifications   NotificationPreferences `json:"notifications"`
	Items           []WorkItem              `json:"items"`
}

type PollResource struct {
	Key                 string
	Revision            int64
	Repository          string
	Kind                ResourceKind
	Number              int
	ETag                string
	Interval            time.Duration
	NextPollAt          time.Time
	LastPollAt          *time.Time
	LastSuccessAt       *time.Time
	LastChangedAt       *time.Time
	ResourceUpdatedAt   time.Time
	UnchangedCount      int
	LastError           string
	NodeID              string
	ItemKind            ItemKind
	LatestReviewComment *Activity
}

func WorkItemsResourceKey(host string) string {
	return host + ":work-items"
}

func ReactionResourceKey(repository string, number int) string {
	return repository + ":pull:" + strconv.Itoa(number) + ":reactions"
}

func ActivityResourceKey(repository string, number int) string {
	return repository + ":item:" + strconv.Itoa(number) + ":activity"
}

func ParseRepositoryKey(key string) (Repository, error) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Repository{}, fmt.Errorf("parse repository key %q", key)
	}
	return Repository{Host: parts[0], Owner: parts[1], Name: parts[2]}, nil
}
