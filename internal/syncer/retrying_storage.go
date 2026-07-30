package syncer

import (
	"context"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

type retryingStorage struct {
	store Storage
	retry func(context.Context, func() error) error
}

func (s *retryingStorage) ListDueResources(
	ctx context.Context,
	scope string,
	now time.Time,
	limit int,
) ([]model.PollResource, error) {
	var resources []model.PollResource
	err := s.retry(ctx, func() error {
		var err error
		resources, err = s.store.ListDueResources(ctx, scope, now, limit)
		return err
	})
	return resources, err
}

func (s *retryingStorage) ReplaceRelevantOpenItems(
	ctx context.Context,
	host string,
	items []model.WorkItem,
	now time.Time,
) (bool, error) {
	var changed bool
	err := s.retry(ctx, func() error {
		var err error
		changed, err = s.store.ReplaceRelevantOpenItems(ctx, host, items, now)
		return err
	})
	return changed, err
}

func (s *retryingStorage) ReplaceReactions(
	ctx context.Context,
	repository string,
	number int,
	expectedRevision int64,
	reactions []model.Reaction,
) (bool, bool, error) {
	var (
		changed bool
		applied bool
	)
	err := s.retry(ctx, func() error {
		var err error
		changed, applied, err = s.store.ReplaceReactions(
			ctx,
			repository,
			number,
			expectedRevision,
			reactions,
		)
		return err
	})
	return changed, applied, err
}

func (s *retryingStorage) ReplaceActivity(
	ctx context.Context,
	repository string,
	number int,
	expectedRevision int64,
	activity *model.Activity,
	latestCommit *model.Activity,
	latestReviewComment *model.Activity,
) (bool, bool, error) {
	var (
		changed bool
		applied bool
	)
	err := s.retry(ctx, func() error {
		var err error
		changed, applied, err = s.store.ReplaceActivity(
			ctx,
			repository,
			number,
			expectedRevision,
			activity,
			latestCommit,
			latestReviewComment,
		)
		return err
	})
	return changed, applied, err
}

func (s *retryingStorage) SavePollResource(
	ctx context.Context,
	resource model.PollResource,
) error {
	return s.retry(ctx, func() error {
		return s.store.SavePollResource(ctx, resource)
	})
}

func (s *retryingStorage) ForceDue(
	ctx context.Context,
	scope string,
	now time.Time,
) error {
	return s.retry(ctx, func() error {
		return s.store.ForceDue(ctx, scope, now)
	})
}

var _ Storage = (*retryingStorage)(nil)
