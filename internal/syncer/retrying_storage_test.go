package syncer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestRetryingStorageRetriesEachOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(context.Context, Storage) error
	}{
		{
			name: "ListDueResources",
			call: func(ctx context.Context, storage Storage) error {
				_, err := storage.ListDueResources(ctx, "", time.Time{}, 1)
				return err
			},
		},
		{
			name: "ReplaceRelevantOpenItems",
			call: func(ctx context.Context, storage Storage) error {
				_, err := storage.ReplaceRelevantOpenItems(
					ctx,
					"",
					nil,
					time.Time{},
				)
				return err
			},
		},
		{
			name: "ReplaceReactions",
			call: func(ctx context.Context, storage Storage) error {
				_, _, err := storage.ReplaceReactions(ctx, "", 0, 0, nil)
				return err
			},
		},
		{
			name: "ReplaceActivity",
			call: func(ctx context.Context, storage Storage) error {
				_, _, err := storage.ReplaceActivity(
					ctx,
					"",
					0,
					0,
					nil,
					nil,
					nil,
				)
				return err
			},
		},
		{
			name: "SavePollResource",
			call: func(ctx context.Context, storage Storage) error {
				return storage.SavePollResource(ctx, model.PollResource{})
			},
		},
		{
			name: "ForceDue",
			call: func(ctx context.Context, storage Storage) error {
				return storage.ForceDue(ctx, "", time.Time{})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			base := &scriptedStorage{
				call: func(method string) error {
					if method != test.name {
						t.Fatalf("storage method = %q, want %q", method, test.name)
					}
					attempts++
					if attempts < 3 {
						return fmt.Errorf("attempt %d failed", attempts)
					}
					return nil
				},
			}
			retryCalls := 0
			retry := func(
				_ context.Context,
				operation func() error,
			) error {
				retryCalls++
				var err error
				for range 3 {
					err = operation()
					if err == nil {
						return nil
					}
				}
				return err
			}
			runner := New(base, nil, "", "", 1, nil, retry)

			if err := test.call(t.Context(), runner.store); err != nil {
				t.Fatalf("%s() error = %v", test.name, err)
			}
			if attempts != 3 {
				t.Fatalf("%s() attempts = %d, want 3", test.name, attempts)
			}
			if retryCalls != 1 {
				t.Fatalf("%s() retry calls = %d, want 1", test.name, retryCalls)
			}
		})
	}
}

type scriptedStorage struct {
	call func(string) error
}

func (s *scriptedStorage) ListDueResources(
	context.Context,
	string,
	time.Time,
	int,
) ([]model.PollResource, error) {
	return nil, s.call("ListDueResources")
}

func (s *scriptedStorage) ReplaceRelevantOpenItems(
	context.Context,
	string,
	[]model.WorkItem,
	time.Time,
) (bool, error) {
	return false, s.call("ReplaceRelevantOpenItems")
}

func (s *scriptedStorage) ReplaceReactions(
	context.Context,
	string,
	int,
	int64,
	[]model.Reaction,
) (bool, bool, error) {
	return false, false, s.call("ReplaceReactions")
}

func (s *scriptedStorage) ReplaceActivity(
	context.Context,
	string,
	int,
	int64,
	*model.Activity,
	*model.Activity,
	*model.Activity,
) (bool, bool, error) {
	return false, false, s.call("ReplaceActivity")
}

func (s *scriptedStorage) SavePollResource(
	context.Context,
	model.PollResource,
) error {
	return s.call("SavePollResource")
}

func (s *scriptedStorage) ForceDue(
	context.Context,
	string,
	time.Time,
) error {
	return s.call("ForceDue")
}
