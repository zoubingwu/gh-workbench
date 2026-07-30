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

const (
	schedulerTick           = 250 * time.Millisecond
	activityBatchSize       = 50
	failuresBeforeReporting = 3
)

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
	FetchLatestActivities(
		context.Context,
		[]model.ActivityTarget,
	) ([]model.ActivityResult, error)
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
	ReplaceActivity(
		context.Context,
		string,
		int,
		int64,
		*model.Activity,
		*model.Activity,
		*model.Activity,
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
	retryStore func(context.Context, func() error) error,
) *Runner {
	if workers < 1 {
		workers = 1
	}
	if onUpdate == nil {
		onUpdate = func() {}
	}
	if retryStore != nil {
		store = &retryingStorage{
			store: store,
			retry: retryStore,
		}
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

	jobs := make(chan pollJob, r.workers)
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

	inFlight := make(map[string]struct{}, r.workers*activityBatchSize)
	inFlightJobs := 0
	dispatch := func(now time.Time) (bool, error) {
		if now.UnixNano() < r.pauseUntil.Load() {
			return false, nil
		}
		free := r.workers - inFlightJobs
		if free <= 0 {
			return false, nil
		}
		due, err := r.store.ListDueResources(
			ctx,
			r.host,
			now,
			r.workers*activityBatchSize,
		)
		if err != nil {
			return false, err
		}

		discoveryStarted := false
		for _, resource := range due {
			if inFlightJobs >= r.workers {
				break
			}
			if _, ok := inFlight[resource.Key]; ok {
				continue
			}

			resources := []model.PollResource{resource}
			if resource.Kind == model.ResourceKindActivity {
				resources = activityBatch(due, inFlight, resource)
			}
			jobs <- pollJob{resources: resources}
			inFlightJobs++
			for _, scheduled := range resources {
				inFlight[scheduled.Key] = struct{}{}
				if scheduled.Kind == model.ResourceKindWorkItems {
					r.discovery.Store(true)
					discoveryStarted = true
				}
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
			discoveryFinished := false
			for _, key := range result.keys {
				delete(inFlight, key)
				if key == model.WorkItemsResourceKey(r.host) {
					discoveryFinished = true
				}
			}
			inFlightJobs--
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
			if result.publish || discoveryFinished || started || inFlightJobs == 0 {
				r.onUpdate()
			}
		}
	}
}

func activityBatch(
	due []model.PollResource,
	inFlight map[string]struct{},
	first model.PollResource,
) []model.PollResource {
	resources := make([]model.PollResource, 0, min(activityBatchSize, len(due)))
	resources = append(resources, first)
	for _, resource := range due {
		if len(resources) == activityBatchSize {
			break
		}
		if resource.Kind != model.ResourceKindActivity || resource.Key == first.Key {
			continue
		}
		if _, ok := inFlight[resource.Key]; ok {
			continue
		}
		resources = append(resources, resource)
	}
	return resources
}

type pollJob struct {
	resources []model.PollResource
}

type workerResult struct {
	keys    []string
	publish bool
	err     error
}

func (r *Runner) worker(
	ctx context.Context,
	jobs <-chan pollJob,
	results chan<- workerResult,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			publish, err := r.pollJob(ctx, job)
			keys := make([]string, 0, len(job.resources))
			for _, resource := range job.resources {
				keys = append(keys, resource.Key)
			}
			select {
			case results <- workerResult{
				keys:    keys,
				publish: publish,
				err:     err,
			}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (r *Runner) pollJob(ctx context.Context, job pollJob) (bool, error) {
	if len(job.resources) == 0 {
		return false, nil
	}
	if job.resources[0].Kind == model.ResourceKindActivity {
		return r.pollActivities(ctx, job.resources)
	}
	return r.poll(ctx, job.resources[0])
}

func (r *Runner) pollActivities(
	ctx context.Context,
	resources []model.PollResource,
) (bool, error) {
	targets := make([]model.ActivityTarget, 0, len(resources))
	for _, resource := range resources {
		repository, err := model.ParseRepositoryKey(resource.Repository)
		if err != nil {
			publish, saveErr := r.saveActivityBatchFailure(ctx, resources, err)
			return publish, errors.Join(err, saveErr)
		}
		targets = append(targets, model.ActivityTarget{
			NodeID:              resource.NodeID,
			Repository:          repository,
			Number:              resource.Number,
			Kind:                resource.ItemKind,
			LatestCommit:        resource.LatestCommit,
			LatestReviewComment: resource.LatestReviewComment,
			ETag:                resource.ETag,
		})
	}

	results, err := r.source.FetchLatestActivities(ctx, targets)
	if err != nil {
		return r.saveActivityBatchFailure(ctx, resources, err)
	}
	if len(results) != len(resources) {
		resultErr := fmt.Errorf(
			"fetch latest activities returned %d results for %d targets",
			len(results),
			len(resources),
		)
		publish, saveErr := r.saveActivityBatchFailure(ctx, resources, resultErr)
		return publish, errors.Join(resultErr, saveErr)
	}

	publish := false
	saveErrors := make([]error, 0)
	for index, resource := range resources {
		result := results[index]
		changed, applied, err := r.store.ReplaceActivity(
			ctx,
			resource.Repository,
			resource.Number,
			resource.Revision,
			result.Activity,
			result.LatestCommit,
			result.LatestReviewComment,
		)
		if err != nil {
			itemPublished, saveErr := r.savePollOutcome(
				ctx,
				resource,
				OutcomeFailed,
				err,
			)
			publish = publish || itemPublished
			saveErrors = append(saveErrors, err)
			if saveErr != nil {
				saveErrors = append(saveErrors, saveErr)
			}
			continue
		}
		if !applied {
			continue
		}

		resource.ETag = result.ETag
		outcome := OutcomeUnchanged
		if changed {
			outcome = OutcomeChanged
		}
		itemPublished, err := r.savePollOutcome(ctx, resource, outcome, nil)
		publish = publish || itemPublished
		if err != nil {
			saveErrors = append(saveErrors, err)
		}
	}
	return publish, errors.Join(saveErrors...)
}

func (r *Runner) saveActivityBatchFailure(
	ctx context.Context,
	resources []model.PollResource,
	pollErr error,
) (bool, error) {
	publish := false
	saveErrors := make([]error, 0)
	for _, resource := range resources {
		itemPublished, err := r.savePollOutcome(
			ctx,
			resource,
			OutcomeFailed,
			pollErr,
		)
		publish = publish || itemPublished
		if err != nil {
			saveErrors = append(saveErrors, err)
		}
	}
	return publish, errors.Join(saveErrors...)
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

	resource.ETag = etag
	publish, err := r.savePollOutcome(ctx, resource, outcome, pollErr)
	if err != nil {
		return false, err
	}
	if storeError {
		return publish, pollErr
	}
	return publish, nil
}

func (r *Runner) savePollOutcome(
	ctx context.Context,
	resource model.PollResource,
	outcome Outcome,
	pollErr error,
) (bool, error) {
	lastError := resource.LastError
	errorWasReported := lastError != ""
	completedAt := time.Now().UTC()
	var retryable interface{ RetryAt() time.Time }
	reportImmediately := errors.As(pollErr, &retryable)
	if reportImmediately {
		r.pause(retryable.RetryAt())
	}
	resource.LastPollAt = &completedAt
	resource.LastError = ""
	switch outcome {
	case OutcomeChanged:
		resource.LastSuccessAt = &completedAt
		resource.LastChangedAt = &completedAt
		resource.UnchangedCount = 0
		resource.FailureCount = 0
	case OutcomeUnchanged:
		resource.LastSuccessAt = &completedAt
		resource.UnchangedCount++
		resource.FailureCount = 0
	case OutcomeFailed:
		resource.FailureCount++
		if errorWasReported ||
			reportImmediately ||
			resource.FailureCount >= failuresBeforeReporting {
			resource.LastError = pollErr.Error()
		}
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
