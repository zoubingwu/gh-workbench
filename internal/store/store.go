package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
	_ "modernc.org/sqlite"
)

const (
	initialInterval          = 10 * time.Second
	missingPollsBeforeDelete = 3
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	database := &Store{db: db}
	if err := database.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return database, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initialize(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	}
	for _, statement := range pragmas {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}

	const schema = `
CREATE TABLE IF NOT EXISTS work_items (
	repository TEXT NOT NULL,
	number INTEGER NOT NULL,
	node_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	title TEXT NOT NULL,
	url TEXT NOT NULL,
	state TEXT NOT NULL,
	author TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	is_draft INTEGER NOT NULL DEFAULT 0,
	review_decision TEXT NOT NULL DEFAULT '',
	merge_state TEXT NOT NULL DEFAULT '',
	needs_review INTEGER NOT NULL DEFAULT 0,
	additions INTEGER NOT NULL DEFAULT 0,
	deletions INTEGER NOT NULL DEFAULT 0,
	labels_json TEXT NOT NULL DEFAULT '[]',
	latest_activity_json TEXT,
	latest_review_comment_json TEXT,
	missing_polls INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (repository, number)
);

CREATE TABLE IF NOT EXISTS reactions (
	repository TEXT NOT NULL,
	number INTEGER NOT NULL,
	id INTEGER NOT NULL,
	content TEXT NOT NULL,
	user TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (repository, number, id),
	FOREIGN KEY (repository, number)
		REFERENCES work_items(repository, number)
		ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS poll_resources (
	resource_key TEXT PRIMARY KEY,
	repository TEXT NOT NULL,
	kind TEXT NOT NULL,
	number INTEGER NOT NULL DEFAULT 0,
	etag TEXT NOT NULL DEFAULT '',
	interval_ns INTEGER NOT NULL,
	next_poll_at INTEGER NOT NULL,
	last_poll_at INTEGER,
	last_success_at INTEGER,
	last_changed_at INTEGER,
	resource_updated_at INTEGER NOT NULL,
	unchanged_count INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	revision INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS notification_preferences (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	enabled INTEGER NOT NULL DEFAULT 0,
	only_my_pull_requests INTEGER NOT NULL DEFAULT 1
);

INSERT OR IGNORE INTO notification_preferences (
	id,
	enabled,
	only_my_pull_requests
) VALUES (1, 0, 1);

CREATE INDEX IF NOT EXISTS poll_resources_due
	ON poll_resources(repository, next_poll_at);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create sqlite schema: %w", err)
	}
	if err := s.migrateWorkItemColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) NotificationPreferences(
	ctx context.Context,
) (model.NotificationPreferences, error) {
	var preferences model.NotificationPreferences
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT enabled, only_my_pull_requests
		FROM notification_preferences
		WHERE id = 1`,
	).Scan(
		&preferences.Enabled,
		&preferences.OnlyMyPullRequests,
	); err != nil {
		return model.NotificationPreferences{}, fmt.Errorf(
			"load notification preferences: %w",
			err,
		)
	}
	return preferences, nil
}

func (s *Store) UpdateNotificationPreferences(
	ctx context.Context,
	update model.NotificationPreferencesUpdate,
) error {
	var (
		statement string
		value     bool
	)
	switch {
	case update.Enabled != nil && update.OnlyMyPullRequests == nil:
		statement = `UPDATE notification_preferences
			SET enabled = ?
			WHERE id = 1`
		value = *update.Enabled
	case update.Enabled == nil && update.OnlyMyPullRequests != nil:
		statement = `UPDATE notification_preferences
			SET only_my_pull_requests = ?
			WHERE id = 1`
		value = *update.OnlyMyPullRequests
	default:
		return errors.New("update exactly one notification preference")
	}
	if _, err := s.db.ExecContext(
		ctx,
		statement,
		value,
	); err != nil {
		return fmt.Errorf("update notification preferences: %w", err)
	}
	return nil
}

func (s *Store) migrateWorkItemColumns(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin work item schema migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(work_items)")
	if err != nil {
		return fmt.Errorf("inspect work item schema: %w", err)
	}
	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			position   int
			name       string
			columnType string
			notNull    int
			defaultSQL sql.NullString
			primaryKey int
		)
		if err := rows.Scan(
			&position,
			&name,
			&columnType,
			&notNull,
			&defaultSQL,
			&primaryKey,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan work item schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate work item schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close work item schema: %w", err)
	}
	_, hadReviewCommentCache := columns["latest_review_comment_json"]
	migrations := []struct {
		name string
		sql  string
	}{
		{name: "node_id", sql: "ALTER TABLE work_items ADD COLUMN node_id TEXT NOT NULL DEFAULT ''"},
		{name: "is_draft", sql: "ALTER TABLE work_items ADD COLUMN is_draft INTEGER NOT NULL DEFAULT 0"},
		{name: "review_decision", sql: "ALTER TABLE work_items ADD COLUMN review_decision TEXT NOT NULL DEFAULT ''"},
		{name: "merge_state", sql: "ALTER TABLE work_items ADD COLUMN merge_state TEXT NOT NULL DEFAULT ''"},
		{name: "needs_review", sql: "ALTER TABLE work_items ADD COLUMN needs_review INTEGER NOT NULL DEFAULT 0"},
		{name: "additions", sql: "ALTER TABLE work_items ADD COLUMN additions INTEGER NOT NULL DEFAULT 0"},
		{name: "deletions", sql: "ALTER TABLE work_items ADD COLUMN deletions INTEGER NOT NULL DEFAULT 0"},
		{name: "labels_json", sql: "ALTER TABLE work_items ADD COLUMN labels_json TEXT NOT NULL DEFAULT '[]'"},
		{name: "latest_activity_json", sql: "ALTER TABLE work_items ADD COLUMN latest_activity_json TEXT"},
		{name: "latest_review_comment_json", sql: "ALTER TABLE work_items ADD COLUMN latest_review_comment_json TEXT"},
		{name: "missing_polls", sql: "ALTER TABLE work_items ADD COLUMN missing_polls INTEGER NOT NULL DEFAULT 0"},
	}
	for _, migration := range migrations {
		if _, ok := columns[migration.name]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("add work item column %q: %w", migration.name, err)
		}
	}
	if !hadReviewCommentCache {
		if _, err := tx.ExecContext(
			ctx,
			"UPDATE poll_resources SET etag = '' WHERE kind = ?",
			model.ResourceKindActivity,
		); err != nil {
			return fmt.Errorf("reset migrated activity ETags: %w", err)
		}
	} else if _, err := tx.ExecContext(
		ctx,
		`UPDATE poll_resources
			SET etag = ''
			WHERE kind = ?
				AND etag <> ''
				AND EXISTS (
					SELECT 1
					FROM work_items
					WHERE work_items.repository = poll_resources.repository
						AND work_items.number = poll_resources.number
						AND work_items.kind = ?
						AND work_items.latest_review_comment_json IS NULL
				)`,
		model.ResourceKindActivity,
		model.ItemKindPullRequest,
	); err != nil {
		return fmt.Errorf("heal activity ETags without review comment cache: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit work item schema migration: %w", err)
	}
	return nil
}

func (s *Store) EnsureAccount(
	ctx context.Context,
	host string,
	now time.Time,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account setup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO poll_resources (
			resource_key,
			repository,
			kind,
			interval_ns,
			next_poll_at,
			resource_updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		model.WorkItemsResourceKey(host),
		host,
		model.ResourceKindWorkItems,
		initialInterval.Nanoseconds(),
		now.UnixNano(),
		now.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("ensure account poll resource: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect account poll resource: %w", err)
	}
	if inserted > 0 {
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM poll_resources
			WHERE (repository = ? OR repository GLOB ?)
				AND resource_key <> ?`,
			host,
			host+"/*",
			model.WorkItemsResourceKey(host),
		); err != nil {
			return fmt.Errorf("remove legacy poll resources: %w", err)
		}
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM work_items WHERE repository = ? OR repository GLOB ?",
			host,
			host+"/*",
		); err != nil {
			return fmt.Errorf("remove legacy work items: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account setup: %w", err)
	}
	return nil
}

type itemRecord struct {
	nodeID            string
	kind              model.ItemKind
	title             string
	url               string
	state             string
	author            string
	createdAt         time.Time
	updatedAt         time.Time
	isDraft           bool
	reviewDecision    string
	mergeState        string
	needsReview       bool
	additions         int
	deletions         int
	labelsJSON        string
	activityJSON      sql.NullString
	reviewCommentJSON sql.NullString
	missingPolls      int
}

func (s *Store) ReplaceRelevantOpenItems(
	ctx context.Context,
	host string,
	items []model.WorkItem,
	now time.Time,
) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin item reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var accountExists int
	err = tx.QueryRowContext(
		ctx,
		`SELECT 1
		FROM poll_resources
		WHERE resource_key = ? AND kind = ?`,
		model.WorkItemsResourceKey(host),
		model.ResourceKindWorkItems,
	).Scan(&accountExists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find account for item reconciliation: %w", err)
	}

	existing, err := loadItemRecords(ctx, tx, host)
	if err != nil {
		return false, err
	}

	changed := false
	seen := make(map[itemIdentity]struct{}, len(items))
	for _, item := range items {
		repository, err := model.ParseRepositoryKey(item.RepositoryKey)
		if err != nil {
			return false, fmt.Errorf("validate work item repository: %w", err)
		}
		if repository.Host != host {
			return false, fmt.Errorf(
				"reconcile work item %q for unexpected host %q",
				item.URL,
				repository.Host,
			)
		}
		if !strings.EqualFold(item.State, "open") {
			return false, fmt.Errorf("reconcile non-open work item %q", item.URL)
		}
		key := itemIdentity{repository: item.RepositoryKey, number: item.Number}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		labels := item.Labels
		if labels == nil {
			labels = make([]model.Label, 0)
		}
		encodedLabels, err := json.Marshal(labels)
		if err != nil {
			return false, fmt.Errorf("encode work item labels for %q: %w", item.URL, err)
		}
		labelsJSON := string(encodedLabels)
		activityJSON, err := encodeActivity(item.LatestActivity)
		if err != nil {
			return false, fmt.Errorf("encode latest activity for %q: %w", item.URL, err)
		}
		reviewCommentJSON, err := encodeActivity(item.LatestReviewComment)
		if err != nil {
			return false, fmt.Errorf(
				"encode latest review comment for %q: %w",
				item.URL,
				err,
			)
		}
		record := itemRecord{
			nodeID:            item.NodeID,
			kind:              item.Kind,
			title:             item.Title,
			url:               item.URL,
			state:             strings.ToLower(item.State),
			author:            item.Author,
			createdAt:         item.CreatedAt,
			updatedAt:         item.UpdatedAt,
			isDraft:           item.IsDraft,
			reviewDecision:    item.ReviewDecision,
			mergeState:        item.MergeState,
			needsReview:       item.NeedsReview,
			additions:         item.Additions,
			deletions:         item.Deletions,
			labelsJSON:        labelsJSON,
			activityJSON:      activityJSON,
			reviewCommentJSON: reviewCommentJSON,
		}
		old, exists := existing[key]
		if !activityJSON.Valid && exists {
			record.activityJSON = old.activityJSON
		}
		if !reviewCommentJSON.Valid && exists {
			record.reviewCommentJSON = old.reviewCommentJSON
		}
		if !exists || old != record {
			changed = true
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO work_items (
				repository,
				number,
				node_id,
				kind,
				title,
				url,
				state,
				author,
				created_at,
				updated_at,
				is_draft,
				review_decision,
				merge_state,
				needs_review,
				additions,
				deletions,
				labels_json,
				latest_activity_json,
				latest_review_comment_json,
				missing_polls
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(repository, number) DO UPDATE SET
				node_id = excluded.node_id,
				kind = excluded.kind,
				title = excluded.title,
				url = excluded.url,
				state = excluded.state,
				author = excluded.author,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				is_draft = excluded.is_draft,
				review_decision = excluded.review_decision,
				merge_state = excluded.merge_state,
				needs_review = excluded.needs_review,
				additions = excluded.additions,
				deletions = excluded.deletions,
				labels_json = excluded.labels_json,
				latest_activity_json = COALESCE(
					excluded.latest_activity_json,
					work_items.latest_activity_json
				),
				latest_review_comment_json = COALESCE(
					excluded.latest_review_comment_json,
					work_items.latest_review_comment_json
				),
				missing_polls = 0`,
			item.RepositoryKey,
			item.Number,
			item.NodeID,
			item.Kind,
			item.Title,
			item.URL,
			strings.ToLower(item.State),
			item.Author,
			item.CreatedAt.UnixNano(),
			item.UpdatedAt.UnixNano(),
			item.IsDraft,
			item.ReviewDecision,
			item.MergeState,
			item.NeedsReview,
			item.Additions,
			item.Deletions,
			labelsJSON,
			activityJSON,
			reviewCommentJSON,
		); err != nil {
			return false, fmt.Errorf("upsert work item %q: %w", item.URL, err)
		}

		if err := ensureActivityResource(
			ctx,
			tx,
			item.RepositoryKey,
			item,
			now,
		); err != nil {
			return false, err
		}
		if item.Kind == model.ItemKindPullRequest {
			if err := ensureReactionResource(
				ctx,
				tx,
				item.RepositoryKey,
				item,
				now,
			); err != nil {
				return false, err
			}
		} else if err := deleteReactionResource(
			ctx,
			tx,
			item.RepositoryKey,
			item.Number,
		); err != nil {
			return false, err
		}
	}

	for key, record := range existing {
		if _, ok := seen[key]; ok {
			continue
		}
		if record.missingPolls == 0 {
			changed = true
		}
		missingPolls := record.missingPolls + 1
		if missingPolls >= missingPollsBeforeDelete {
			if err := deleteActivityResource(ctx, tx, key.repository, key.number); err != nil {
				return false, err
			}
			if err := deleteReactionResource(ctx, tx, key.repository, key.number); err != nil {
				return false, err
			}
			if _, err := tx.ExecContext(
				ctx,
				"DELETE FROM work_items WHERE repository = ? AND number = ?",
				key.repository,
				key.number,
			); err != nil {
				return false, fmt.Errorf(
					"delete missing work item %s#%d: %w",
					key.repository,
					key.number,
					err,
				)
			}
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE work_items
			SET missing_polls = ?
			WHERE repository = ? AND number = ?`,
			missingPolls,
			key.repository,
			key.number,
		); err != nil {
			return false, fmt.Errorf(
				"mark missing work item %s#%d: %w",
				key.repository,
				key.number,
				err,
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit item reconciliation: %w", err)
	}
	return changed, nil
}

func loadItemRecords(
	ctx context.Context,
	tx *sql.Tx,
	host string,
) (map[itemIdentity]itemRecord, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT
			repository,
			number,
			node_id,
			kind,
			title,
			url,
			state,
			author,
			created_at,
			updated_at,
			is_draft,
			review_decision,
			merge_state,
			needs_review,
			additions,
			deletions,
			labels_json,
			latest_activity_json,
			latest_review_comment_json,
			missing_polls
		FROM work_items
		WHERE repository = ? OR repository GLOB ?`,
		host,
		host+"/*",
	)
	if err != nil {
		return nil, fmt.Errorf("load existing work items: %w", err)
	}
	defer rows.Close()

	records := make(map[itemIdentity]itemRecord)
	for rows.Next() {
		var (
			key       itemIdentity
			record    itemRecord
			createdAt int64
			updatedAt int64
		)
		if err := rows.Scan(
			&key.repository,
			&key.number,
			&record.nodeID,
			&record.kind,
			&record.title,
			&record.url,
			&record.state,
			&record.author,
			&createdAt,
			&updatedAt,
			&record.isDraft,
			&record.reviewDecision,
			&record.mergeState,
			&record.needsReview,
			&record.additions,
			&record.deletions,
			&record.labelsJSON,
			&record.activityJSON,
			&record.reviewCommentJSON,
			&record.missingPolls,
		); err != nil {
			return nil, fmt.Errorf("scan existing work item: %w", err)
		}
		record.createdAt = time.Unix(0, createdAt).UTC()
		record.updatedAt = time.Unix(0, updatedAt).UTC()
		records[key] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing work items: %w", err)
	}
	return records, nil
}

func ensureActivityResource(
	ctx context.Context,
	tx *sql.Tx,
	repository string,
	item model.WorkItem,
	now time.Time,
) error {
	return ensureItemResource(ctx, tx, model.PollResource{
		Key:               model.ActivityResourceKey(repository, item.Number),
		Repository:        repository,
		Kind:              model.ResourceKindActivity,
		Number:            item.Number,
		Interval:          initialInterval,
		NextPollAt:        now,
		ResourceUpdatedAt: item.UpdatedAt,
	})
}

func ensureReactionResource(
	ctx context.Context,
	tx *sql.Tx,
	repository string,
	item model.WorkItem,
	now time.Time,
) error {
	return ensureItemResource(ctx, tx, model.PollResource{
		Key:               model.ReactionResourceKey(repository, item.Number),
		Repository:        repository,
		Kind:              model.ResourceKindReactions,
		Number:            item.Number,
		Interval:          initialInterval,
		NextPollAt:        now,
		ResourceUpdatedAt: item.UpdatedAt,
	})
}

func ensureItemResource(
	ctx context.Context,
	tx *sql.Tx,
	resource model.PollResource,
) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO poll_resources (
			resource_key,
			repository,
			kind,
			number,
			interval_ns,
			next_poll_at,
			resource_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(resource_key) DO UPDATE SET
			interval_ns = CASE
				WHEN excluded.resource_updated_at > poll_resources.resource_updated_at
					THEN excluded.interval_ns
				ELSE poll_resources.interval_ns
			END,
			next_poll_at = CASE
				WHEN excluded.resource_updated_at > poll_resources.resource_updated_at
					THEN excluded.next_poll_at
				ELSE poll_resources.next_poll_at
			END,
			resource_updated_at = excluded.resource_updated_at,
			unchanged_count = CASE
				WHEN excluded.resource_updated_at > poll_resources.resource_updated_at
					THEN 0
				ELSE poll_resources.unchanged_count
			END,
			revision = CASE
				WHEN excluded.resource_updated_at > poll_resources.resource_updated_at
					THEN poll_resources.revision + 1
				ELSE poll_resources.revision
			END`,
		resource.Key,
		resource.Repository,
		resource.Kind,
		resource.Number,
		resource.Interval.Nanoseconds(),
		resource.NextPollAt.UnixNano(),
		resource.ResourceUpdatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf(
			"ensure %s resource for item %d: %w",
			resource.Kind,
			resource.Number,
			err,
		)
	}
	return nil
}

func deleteActivityResource(
	ctx context.Context,
	tx *sql.Tx,
	repository string,
	number int,
) error {
	_, err := tx.ExecContext(
		ctx,
		"DELETE FROM poll_resources WHERE resource_key = ?",
		model.ActivityResourceKey(repository, number),
	)
	if err != nil {
		return fmt.Errorf("delete activity resource for item %d: %w", number, err)
	}
	return nil
}

func deleteReactionResource(
	ctx context.Context,
	tx *sql.Tx,
	repository string,
	number int,
) error {
	_, err := tx.ExecContext(
		ctx,
		"DELETE FROM poll_resources WHERE resource_key = ?",
		model.ReactionResourceKey(repository, number),
	)
	if err != nil {
		return fmt.Errorf("delete reaction resource for item %d: %w", number, err)
	}
	return nil
}

func (s *Store) ReplaceReactions(
	ctx context.Context,
	repository string,
	number int,
	expectedRevision int64,
	reactions []model.Reaction,
) (bool, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, fmt.Errorf("begin reaction reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var revision int64
	err = tx.QueryRowContext(
		ctx,
		`SELECT poll_resources.revision
		FROM work_items
		JOIN poll_resources ON poll_resources.resource_key = ?
		WHERE work_items.repository = ?
			AND work_items.number = ?
			AND poll_resources.kind = ?`,
		model.ReactionResourceKey(repository, number),
		repository,
		number,
		model.ResourceKindReactions,
	).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf(
			"find poll resource for item %d reactions: %w",
			number,
			err,
		)
	}
	if revision != expectedRevision {
		return false, false, nil
	}

	existing, err := loadReactions(ctx, tx, repository, number)
	if err != nil {
		return false, false, err
	}
	slices.SortFunc(reactions, func(a, b model.Reaction) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	if slices.Equal(existing, reactions) {
		return false, true, nil
	}

	if _, err := tx.ExecContext(
		ctx,
		"DELETE FROM reactions WHERE repository = ? AND number = ?",
		repository,
		number,
	); err != nil {
		return false, false, fmt.Errorf("delete old reactions for item %d: %w", number, err)
	}
	for _, reaction := range reactions {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO reactions (
				repository,
				number,
				id,
				content,
				user,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			repository,
			number,
			reaction.ID,
			reaction.Content,
			reaction.User,
			reaction.CreatedAt.UnixNano(),
		); err != nil {
			return false, false, fmt.Errorf("insert reaction %d: %w", reaction.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("commit reaction reconciliation: %w", err)
	}
	return true, true, nil
}

func (s *Store) ReplaceActivity(
	ctx context.Context,
	repository string,
	number int,
	expectedRevision int64,
	activity *model.Activity,
	latestReviewComment *model.Activity,
) (bool, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, fmt.Errorf("begin activity reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		revision              int64
		existingActivity      sql.NullString
		existingReviewComment sql.NullString
	)
	err = tx.QueryRowContext(
		ctx,
		`SELECT
			poll_resources.revision,
			work_items.latest_activity_json,
			work_items.latest_review_comment_json
		FROM work_items
		JOIN poll_resources ON poll_resources.resource_key = ?
		WHERE work_items.repository = ?
			AND work_items.number = ?
			AND poll_resources.kind = ?`,
		model.ActivityResourceKey(repository, number),
		repository,
		number,
		model.ResourceKindActivity,
	).Scan(&revision, &existingActivity, &existingReviewComment)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf(
			"find poll resource for item %d activity: %w",
			number,
			err,
		)
	}
	if revision != expectedRevision {
		return false, false, nil
	}

	encodedActivity, err := encodeActivity(activity)
	if err != nil {
		return false, false, fmt.Errorf("encode activity for item %d: %w", number, err)
	}
	encodedReviewComment := sql.NullString{String: "null", Valid: true}
	if latestReviewComment != nil {
		encodedReviewComment, err = encodeActivity(latestReviewComment)
		if err != nil {
			return false, false, fmt.Errorf(
				"encode review comment for item %d: %w",
				number,
				err,
			)
		}
	}
	activityChanged := existingActivity != encodedActivity
	if !activityChanged && existingReviewComment == encodedReviewComment {
		return false, true, nil
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE work_items
		SET
			latest_activity_json = ?,
			latest_review_comment_json = ?
		WHERE repository = ? AND number = ?`,
		encodedActivity,
		encodedReviewComment,
		repository,
		number,
	); err != nil {
		return false, false, fmt.Errorf("update activity for item %d: %w", number, err)
	}
	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("commit activity reconciliation: %w", err)
	}
	return activityChanged, true, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadReactions(
	ctx context.Context,
	query queryer,
	repository string,
	number int,
) ([]model.Reaction, error) {
	rows, err := query.QueryContext(
		ctx,
		`SELECT id, content, user, created_at
		FROM reactions
		WHERE repository = ? AND number = ?
		ORDER BY id`,
		repository,
		number,
	)
	if err != nil {
		return nil, fmt.Errorf("load reactions for item %d: %w", number, err)
	}
	defer rows.Close()

	reactions := make([]model.Reaction, 0)
	for rows.Next() {
		var (
			reaction  model.Reaction
			createdAt int64
		)
		if err := rows.Scan(
			&reaction.ID,
			&reaction.Content,
			&reaction.User,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan reaction for item %d: %w", number, err)
		}
		reaction.CreatedAt = time.Unix(0, createdAt).UTC()
		reactions = append(reactions, reaction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reactions for item %d: %w", number, err)
	}
	return reactions, nil
}

func (s *Store) ListDueResources(
	ctx context.Context,
	scope string,
	now time.Time,
	limit int,
) ([]model.PollResource, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			poll_resources.resource_key,
			poll_resources.repository,
			poll_resources.kind,
			poll_resources.number,
			poll_resources.etag,
			poll_resources.interval_ns,
			poll_resources.next_poll_at,
			poll_resources.last_poll_at,
			poll_resources.last_success_at,
			poll_resources.last_changed_at,
			poll_resources.resource_updated_at,
			poll_resources.unchanged_count,
			poll_resources.last_error,
			poll_resources.revision,
			work_items.node_id,
			work_items.kind,
			work_items.latest_review_comment_json
		FROM poll_resources
		LEFT JOIN work_items
			ON work_items.repository = poll_resources.repository
			AND work_items.number = poll_resources.number
		WHERE (
			poll_resources.repository = ?
			OR poll_resources.repository GLOB ?
		) AND poll_resources.next_poll_at <= ?
			ORDER BY
				poll_resources.next_poll_at,
				CASE poll_resources.kind
					WHEN ? THEN 0
					ELSE 1
				END,
				poll_resources.resource_key
		LIMIT ?`,
		scope,
		scope+"/*",
		now.UnixNano(),
		model.ResourceKindWorkItems,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due poll resources: %w", err)
	}
	defer rows.Close()

	resources := make([]model.PollResource, 0, limit)
	for rows.Next() {
		resource, err := scanPollResource(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due poll resources: %w", err)
	}
	return resources, nil
}

func (s *Store) SavePollResource(
	ctx context.Context,
	resource model.PollResource,
) error {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE poll_resources SET
			etag = ?,
			interval_ns = ?,
			next_poll_at = ?,
			last_poll_at = ?,
			last_success_at = ?,
			last_changed_at = ?,
			resource_updated_at = ?,
			unchanged_count = ?,
			last_error = ?,
			revision = revision + 1
		WHERE resource_key = ? AND revision = ?`,
		resource.ETag,
		resource.Interval.Nanoseconds(),
		resource.NextPollAt.UnixNano(),
		nullableTime(resource.LastPollAt),
		nullableTime(resource.LastSuccessAt),
		nullableTime(resource.LastChangedAt),
		resource.ResourceUpdatedAt.UnixNano(),
		resource.UnchangedCount,
		resource.LastError,
		resource.Key,
		resource.Revision,
	)
	if err != nil {
		return fmt.Errorf("save poll resource %q: %w", resource.Key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect saved poll resource %q: %w", resource.Key, err)
	}
	if affected == 0 {
		return nil
	}
	return nil
}

func (s *Store) ForceDue(
	ctx context.Context,
	scope string,
	now time.Time,
) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE poll_resources
		SET next_poll_at = ?, revision = revision + 1
		WHERE (repository = ? OR repository GLOB ?)`,
		now.UnixNano(),
		scope,
		scope+"/*",
	)
	if err != nil {
		return fmt.Errorf("force poll resources due: %w", err)
	}
	return nil
}

func (s *Store) Snapshot(
	ctx context.Context,
	scope string,
	running bool,
	now time.Time,
) (model.Snapshot, error) {
	items, err := s.loadItems(ctx, scope, 0)
	if err != nil {
		return model.Snapshot{}, err
	}
	resources, err := s.loadResources(ctx, scope)
	if err != nil {
		return model.Snapshot{}, err
	}
	preferences, err := s.NotificationPreferences(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}

	syncResource := resources[model.WorkItemsResourceKey(scope)]
	repositories := make(map[string]struct{})
	for index := range items {
		repositories[items[index].RepositoryKey] = struct{}{}
		resource := syncResource
		if items[index].Kind == model.ItemKindPullRequest {
			key := model.ReactionResourceKey(
				items[index].RepositoryKey,
				items[index].Number,
			)
			if reactions, ok := resources[key]; ok {
				resource = reactions
			}
		}
		items[index].Poll = pollStatus(resource)
	}

	lastSuccess := syncResource.LastSuccessAt
	syncError := syncResource.LastError
	itemErrorKey := ""
	for key, resource := range resources {
		if syncError != "" ||
			resource.Kind == model.ResourceKindWorkItems ||
			resource.LastError == "" ||
			(itemErrorKey != "" && key >= itemErrorKey) {
			continue
		}
		itemErrorKey = key
		syncError = resource.LastError
		if repository, err := model.ParseRepositoryKey(resource.Repository); err == nil {
			syncError = repository.FullName() + ": " + syncError
		}
	}

	return model.Snapshot{
		Host:            scope,
		RepositoryCount: len(repositories),
		GeneratedAt:     now,
		Sync: model.SyncStatus{
			Running:     running,
			LastSuccess: lastSuccess,
			Error:       syncError,
		},
		Notifications: preferences,
		Items:         items,
	}, nil
}

func (s *Store) NotificationBaselineItems(
	ctx context.Context,
	scope string,
) ([]model.WorkItem, error) {
	return s.loadItems(ctx, scope, missingPollsBeforeDelete-1)
}

func (s *Store) loadItems(
	ctx context.Context,
	scope string,
	maxMissingPolls int,
) ([]model.WorkItem, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			repository,
			number,
			node_id,
			kind,
			title,
			url,
			state,
			author,
			created_at,
			updated_at,
			is_draft,
			review_decision,
			merge_state,
			needs_review,
			additions,
			deletions,
			labels_json,
			latest_activity_json
		FROM work_items
		WHERE (repository = ? OR repository GLOB ?)
			AND missing_polls <= ?
		ORDER BY updated_at DESC, number DESC`,
		scope,
		scope+"/*",
		maxMissingPolls,
	)
	if err != nil {
		return nil, fmt.Errorf("load snapshot work items: %w", err)
	}

	items := make([]model.WorkItem, 0)
	for rows.Next() {
		var (
			item         model.WorkItem
			createdAt    int64
			updatedAt    int64
			labelsJSON   string
			activityJSON sql.NullString
		)
		if err := rows.Scan(
			&item.RepositoryKey,
			&item.Number,
			&item.NodeID,
			&item.Kind,
			&item.Title,
			&item.URL,
			&item.State,
			&item.Author,
			&createdAt,
			&updatedAt,
			&item.IsDraft,
			&item.ReviewDecision,
			&item.MergeState,
			&item.NeedsReview,
			&item.Additions,
			&item.Deletions,
			&labelsJSON,
			&activityJSON,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan snapshot work item: %w", err)
		}
		item.CreatedAt = time.Unix(0, createdAt).UTC()
		item.UpdatedAt = time.Unix(0, updatedAt).UTC()
		if err := json.Unmarshal([]byte(labelsJSON), &item.Labels); err != nil {
			rows.Close()
			return nil, fmt.Errorf(
				"decode snapshot work item labels for %s#%d: %w",
				item.RepositoryKey,
				item.Number,
				err,
			)
		}
		if item.Labels == nil {
			item.Labels = make([]model.Label, 0)
		}
		item.LatestActivity, err = decodeActivity(activityJSON)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf(
				"decode snapshot activity for %s#%d: %w",
				item.RepositoryKey,
				item.Number,
				err,
			)
		}
		repository, err := model.ParseRepositoryKey(item.RepositoryKey)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode snapshot repository: %w", err)
		}
		item.Repository = repository.FullName()
		item.Reactions = make([]model.Reaction, 0)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate snapshot work items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close snapshot work items: %w", err)
	}

	reactions, err := s.loadAllReactions(ctx, scope)
	if err != nil {
		return nil, err
	}
	for index := range items {
		key := itemIdentity{
			repository: items[index].RepositoryKey,
			number:     items[index].Number,
		}
		items[index].Reactions = reactions[key]
		if items[index].Reactions == nil {
			items[index].Reactions = make([]model.Reaction, 0)
		}
	}
	return items, nil
}

type itemIdentity struct {
	repository string
	number     int
}

func (s *Store) loadAllReactions(
	ctx context.Context,
	scope string,
) (map[itemIdentity][]model.Reaction, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT repository, number, id, content, user, created_at
		FROM reactions
		WHERE repository = ? OR repository GLOB ?
		ORDER BY repository, number, id`,
		scope,
		scope+"/*",
	)
	if err != nil {
		return nil, fmt.Errorf("load snapshot reactions: %w", err)
	}
	defer rows.Close()

	reactions := make(map[itemIdentity][]model.Reaction)
	for rows.Next() {
		var (
			key       itemIdentity
			reaction  model.Reaction
			createdAt int64
		)
		if err := rows.Scan(
			&key.repository,
			&key.number,
			&reaction.ID,
			&reaction.Content,
			&reaction.User,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan snapshot reaction: %w", err)
		}
		reaction.CreatedAt = time.Unix(0, createdAt).UTC()
		reactions[key] = append(reactions[key], reaction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshot reactions: %w", err)
	}
	return reactions, nil
}

func (s *Store) loadResources(
	ctx context.Context,
	scope string,
) (map[string]model.PollResource, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT
			poll_resources.resource_key,
			poll_resources.repository,
			poll_resources.kind,
			poll_resources.number,
			poll_resources.etag,
			poll_resources.interval_ns,
			poll_resources.next_poll_at,
			poll_resources.last_poll_at,
			poll_resources.last_success_at,
			poll_resources.last_changed_at,
			poll_resources.resource_updated_at,
			poll_resources.unchanged_count,
			poll_resources.last_error,
			poll_resources.revision,
			work_items.node_id,
			work_items.kind,
			work_items.latest_review_comment_json
		FROM poll_resources
		LEFT JOIN work_items
			ON work_items.repository = poll_resources.repository
			AND work_items.number = poll_resources.number
		WHERE poll_resources.repository = ?
			OR poll_resources.repository GLOB ?`,
		scope,
		scope+"/*",
	)
	if err != nil {
		return nil, fmt.Errorf("load snapshot poll resources: %w", err)
	}
	defer rows.Close()

	resources := make(map[string]model.PollResource)
	for rows.Next() {
		resource, err := scanPollResource(rows)
		if err != nil {
			return nil, err
		}
		resources[resource.Key] = resource
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshot poll resources: %w", err)
	}
	return resources, nil
}

type scanner interface {
	Scan(...any) error
}

func scanPollResource(row scanner) (model.PollResource, error) {
	var (
		resource          model.PollResource
		interval          int64
		nextPollAt        int64
		lastPollAt        sql.NullInt64
		lastSuccessAt     sql.NullInt64
		lastChangedAt     sql.NullInt64
		resourceUpdatedAt int64
		nodeID            sql.NullString
		itemKind          sql.NullString
		reviewCommentJSON sql.NullString
	)
	if err := row.Scan(
		&resource.Key,
		&resource.Repository,
		&resource.Kind,
		&resource.Number,
		&resource.ETag,
		&interval,
		&nextPollAt,
		&lastPollAt,
		&lastSuccessAt,
		&lastChangedAt,
		&resourceUpdatedAt,
		&resource.UnchangedCount,
		&resource.LastError,
		&resource.Revision,
		&nodeID,
		&itemKind,
		&reviewCommentJSON,
	); err != nil {
		return model.PollResource{}, fmt.Errorf("scan poll resource: %w", err)
	}
	resource.Interval = time.Duration(interval)
	resource.NextPollAt = time.Unix(0, nextPollAt).UTC()
	resource.LastPollAt = pointerTime(lastPollAt)
	resource.LastSuccessAt = pointerTime(lastSuccessAt)
	resource.LastChangedAt = pointerTime(lastChangedAt)
	resource.ResourceUpdatedAt = time.Unix(0, resourceUpdatedAt).UTC()
	resource.NodeID = nodeID.String
	resource.ItemKind = model.ItemKind(itemKind.String)
	reviewComment, err := decodeActivity(reviewCommentJSON)
	if err != nil {
		return model.PollResource{}, fmt.Errorf(
			"decode poll resource %q review comment: %w",
			resource.Key,
			err,
		)
	}
	resource.LatestReviewComment = reviewComment
	return resource, nil
}

func pollStatus(resource model.PollResource) model.PollStatus {
	return model.PollStatus{
		IntervalSeconds: int64(resource.Interval / time.Second),
		NextPollAt:      resource.NextPollAt,
		LastPollAt:      resource.LastPollAt,
		LastChangedAt:   resource.LastChangedAt,
		UnchangedCount:  resource.UnchangedCount,
		Error:           resource.LastError,
	}
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UnixNano()
}

func pointerTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed := time.Unix(0, value.Int64).UTC()
	return &parsed
}

func encodeActivity(activity *model.Activity) (sql.NullString, error) {
	if activity == nil {
		return sql.NullString{}, nil
	}
	encoded, err := json.Marshal(activity)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(encoded), Valid: true}, nil
}

func decodeActivity(encoded sql.NullString) (*model.Activity, error) {
	if !encoded.Valid {
		return nil, nil
	}
	if strings.TrimSpace(encoded.String) == "null" {
		return nil, nil
	}
	var activity model.Activity
	if err := json.Unmarshal([]byte(encoded.String), &activity); err != nil {
		return nil, err
	}
	return &activity, nil
}
