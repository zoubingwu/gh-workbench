package syncer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

const schedulerTick = 250 * time.Millisecond

type Source interface {
	FetchRelevantOpenItems(
		context.Context,
		string,
		string,
	) (model.ItemsResult, error)
	FetchReactions(
		context.Context,
		model.Repository,
		int,
		string,
	) (model.ReactionsResult, error)
}

type Storage interface {
	ListDueResources(
		context.Context,
		string,
		time.Time,
		int,
	) ([]model.PollResource, error)
	ReplaceRelevantOpenItems(
		context.Context,
		string,
		[]model.WorkItem,
		time.Time,
	) (bool, error)
	ReplaceReactions(
		context.Context,
		string,
		int,
		int64,
		[]model.Reaction,
	) (bool, bool, error)
	SavePollResource(context.Context, model.PollResource) error
	ForceDue(context.Context, string, time.Time) error
}

type Runner struct {
	store      Storage
	source     Source
	host       string
	viewer     string
	workers    int
	onUpdate   func()
	trigger    chan struct{}
	discovery  atomic.Bool
	pauseUntil atomic.Int64
}

func New(
	store Storage,
	source Source,
	host string,
	viewer string,
	workers int,
	onUpdate func(),
) *Runner {
	if workers < 1 {
		workers = 1
	}
	if onUpdate == nil {
		onUpdate = func() {}
	}
	return &Runner{
		store:    store,
		source:   source,
		host:     host,
		viewer:   viewer,
		workers:  workers,
		onUpdate: onUpdate,
		trigger:  make(chan struct{}, 1),
	}
}

func (r *Runner) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

func (r *Runner) Running() bool {
	return r.discovery.Load()
}

func (r *Runner) Run(ctx context.Context) error {
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	jobs := make(chan model.PollResource, r.workers)
	results := make(chan workerResult, r.workers)
	var workers sync.WaitGroup
	for range r.workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r.worker(workerCtx, jobs, results)
		}()
	}
	stopWorkers := func() {
		cancelWorkers()
		close(jobs)
		workers.Wait()
		r.discovery.Store(false)
	}

	inFlight := make(map[string]struct{}, r.workers)
	dispatch := func(now time.Time) (bool, error) {
		if now.UnixNano() < r.pauseUntil.Load() {
			return false, nil
		}
		free := r.workers - len(inFlight)
		if free <= 0 {
			return false, nil
		}
		due, err := r.store.ListDueResources(
			ctx,
			r.host,
			now,
			r.workers*4,
		)
		if err != nil {
			return false, err
		}

		discoveryStarted := false
		for _, resource := range due {
			if len(inFlight) >= r.workers {
				break
			}
			if _, ok := inFlight[resource.Key]; ok {
				continue
			}
			jobs <- resource
			inFlight[resource.Key] = struct{}{}
			if resource.Kind == model.ResourceKindWorkItems {
				r.discovery.Store(true)
				discoveryStarted = true
			}
		}
		return discoveryStarted, nil
	}

	started, err := dispatch(time.Now().UTC())
	if err != nil {
		stopWorkers()
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("dispatch initial poll resources: %w", err)
	}
	if started {
		r.onUpdate()
	}

	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			stopWorkers()
			return nil
		case <-r.trigger:
			if err := r.store.ForceDue(ctx, r.host, time.Now().UTC()); err != nil {
				stopWorkers()
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			started, err := dispatch(time.Now().UTC())
			if err != nil {
				stopWorkers()
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if started {
				r.onUpdate()
			}
		case now := <-ticker.C:
			started, err := dispatch(now.UTC())
			if err != nil {
				stopWorkers()
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if started {
				r.onUpdate()
			}
		case result := <-results:
			delete(inFlight, result.key)
			discoveryFinished := result.key == model.WorkItemsResourceKey(r.host)
			if discoveryFinished {
				r.discovery.Store(false)
			}
			if ctx.Err() != nil {
				stopWorkers()
				return nil
			}
			if result.err != nil {
				stopWorkers()
				return result.err
			}
			started, err := dispatch(time.Now().UTC())
			if err != nil {
				stopWorkers()
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if result.publish || discoveryFinished || started || len(inFlight) == 0 {
				r.onUpdate()
			}
		}
	}
}

type workerResult struct {
	key     string
	publish bool
	err     error
}

func (r *Runner) worker(
	ctx context.Context,
	jobs <-chan model.PollResource,
	results chan<- workerResult,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case resource, ok := <-jobs:
			if !ok {
				return
			}
			publish, err := r.poll(ctx, resource)
			select {
			case results <- workerResult{
				key:     resource.Key,
				publish: publish,
				err:     err,
			}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (r *Runner) poll(
	ctx context.Context,
	resource model.PollResource,
) (bool, error) {
	var (
		outcome    = OutcomeUnchanged
		pollErr    error
		storeError bool
		stale      bool
		etag       = resource.ETag
		lastError  = resource.LastError
	)

	switch resource.Kind {
	case model.ResourceKindWorkItems:
		result, err := r.source.FetchRelevantOpenItems(ctx, r.host, r.viewer)
		switch {
		case err != nil:
			outcome = OutcomeFailed
			pollErr = err
		case result.Unchanged:
			if result.ETag != "" {
				etag = result.ETag
			}
		default:
			changed, err := r.store.ReplaceRelevantOpenItems(
				ctx,
				r.host,
				result.Items,
				time.Now().UTC(),
			)
			if err != nil {
				outcome = OutcomeFailed
				pollErr = err
				storeError = true
				break
			}
			if changed {
				outcome = OutcomeChanged
			}
			etag = result.ETag
			resource.ResourceUpdatedAt = latestItemUpdate(
				resource.ResourceUpdatedAt,
				result.Items,
			)
		}
	case model.ResourceKindReactions:
		repository, err := model.ParseRepositoryKey(resource.Repository)
		if err != nil {
			outcome = OutcomeFailed
			pollErr = err
			storeError = true
			break
		}
		result, err := r.source.FetchReactions(
			ctx,
			repository,
			resource.Number,
			resource.ETag,
		)
		switch {
		case err != nil:
			outcome = OutcomeFailed
			pollErr = err
		case result.Unchanged:
			if result.ETag != "" {
				etag = result.ETag
			}
		default:
			changed, applied, err := r.store.ReplaceReactions(
				ctx,
				repository.Key(),
				resource.Number,
				resource.Revision,
				result.Reactions,
			)
			if err != nil {
				outcome = OutcomeFailed
				pollErr = err
				storeError = true
				break
			}
			if !applied {
				stale = true
				break
			}
			if changed {
				outcome = OutcomeChanged
			}
			etag = result.ETag
		}
	default:
		outcome = OutcomeFailed
		pollErr = fmt.Errorf("poll resource %q has unknown kind %q", resource.Key, resource.Kind)
		storeError = true
	}

	if stale {
		return false, nil
	}

	completedAt := time.Now().UTC()
	var retryable interface{ RetryAt() time.Time }
	if errors.As(pollErr, &retryable) {
		r.pause(retryable.RetryAt())
	}
	resource.ETag = etag
	resource.LastPollAt = &completedAt
	resource.LastError = ""
	switch outcome {
	case OutcomeChanged:
		resource.LastSuccessAt = &completedAt
		resource.LastChangedAt = &completedAt
		resource.UnchangedCount = 0
	case OutcomeUnchanged:
		resource.LastSuccessAt = &completedAt
		resource.UnchangedCount++
	case OutcomeFailed:
		resource.LastError = pollErr.Error()
	}

	activityAt := resource.ResourceUpdatedAt
	if resource.LastChangedAt != nil && resource.LastChangedAt.After(activityAt) {
		activityAt = *resource.LastChangedAt
	}
	resource.Interval = NextInterval(completedAt, activityAt, resource.Interval, outcome)
	resource.NextPollAt = completedAt.Add(resource.Interval)
	if pauseUntil := time.Unix(0, r.pauseUntil.Load()); pauseUntil.After(resource.NextPollAt) {
		resource.NextPollAt = pauseUntil
	}

	if err := r.store.SavePollResource(ctx, resource); err != nil {
		return false, err
	}
	publish := outcome == OutcomeChanged || resource.LastError != lastError
	if storeError {
		return publish, pollErr
	}
	return publish, nil
}

func latestItemUpdate(
	current time.Time,
	items []model.WorkItem,
) time.Time {
	for _, item := range items {
		if item.UpdatedAt.After(current) {
			current = item.UpdatedAt
		}
	}
	return current
}

func (r *Runner) pause(until time.Time) {
	next := until.UnixNano()
	for {
		current := r.pauseUntil.Load()
		if next <= current || r.pauseUntil.CompareAndSwap(current, next) {
			return
		}
	}
}
