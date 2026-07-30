package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestIsBusyCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code int
		want bool
	}{
		{
			name: "busy",
			code: sqlite3.SQLITE_BUSY,
			want: true,
		},
		{
			name: "busy extended",
			code: sqlite3.SQLITE_BUSY | 2<<8,
			want: true,
		},
		{
			name: "locked",
			code: sqlite3.SQLITE_LOCKED,
			want: true,
		},
		{
			name: "locked extended",
			code: sqlite3.SQLITE_LOCKED | 1<<8,
			want: true,
		},
		{
			name: "constraint",
			code: sqlite3.SQLITE_CONSTRAINT,
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := isBusyCode(test.code); got != test.want {
				t.Fatalf("isBusyCode(%d) = %t, want %t", test.code, got, test.want)
			}
		})
	}
}

func TestIsBusyFindsWrappedSQLiteError(t *testing.T) {
	t.Parallel()

	busyErr := createBusyError(t)
	err := fmt.Errorf("save poll resource: %w", errors.Join(
		errors.New("secondary error"),
		busyErr,
	))
	if !IsBusy(err) {
		t.Fatalf("IsBusy(%v) = false, want true", err)
	}
	if IsBusy(errors.New("database is busy")) {
		t.Fatal("IsBusy() matched an untyped error")
	}
}

func TestRetryBusy(t *testing.T) {
	t.Parallel()

	busyErr := createBusyError(t)
	ordinaryErr := errors.New("write failed")

	t.Run("succeeds after retries", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			attempts := 0
			err := RetryBusy(t.Context(), func() error {
				attempts++
				if attempts < 3 {
					return busyErr
				}
				return nil
			})
			if err != nil {
				t.Fatalf("RetryBusy() error = %v", err)
			}
			if attempts != 3 {
				t.Fatalf("RetryBusy() attempts = %d, want 3", attempts)
			}
		})
	})

	t.Run("returns non-retryable error", func(t *testing.T) {
		attempts := 0
		err := RetryBusy(t.Context(), func() error {
			attempts++
			return ordinaryErr
		})
		if !errors.Is(err, ordinaryErr) {
			t.Fatalf("RetryBusy() error = %v, want %v", err, ordinaryErr)
		}
		if attempts != 1 {
			t.Fatalf("RetryBusy() attempts = %d, want 1", attempts)
		}
	})

	t.Run("returns final busy error", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			attempts := 0
			err := RetryBusy(t.Context(), func() error {
				attempts++
				return busyErr
			})
			if !errors.Is(err, busyErr) {
				t.Fatalf("RetryBusy() error = %v, want %v", err, busyErr)
			}
			if attempts != 3 {
				t.Fatalf("RetryBusy() attempts = %d, want 3", attempts)
			}
		})
	})

	t.Run("stops during backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		attempts := 0
		err := RetryBusy(ctx, func() error {
			attempts++
			cancel()
			return busyErr
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RetryBusy() error = %v, want context canceled", err)
		}
		if attempts != 1 {
			t.Fatalf("RetryBusy() attempts = %d, want 1", attempts)
		}
	})
}

func createBusyError(t *testing.T) error {
	t.Helper()

	path := t.TempDir() + "/busy.db"
	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Errorf("Close(first) error = %v", err)
		}
	}()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close(second) error = %v", err)
		}
	}()
	if _, err := second.db.ExecContext(
		t.Context(),
		"PRAGMA busy_timeout = 0",
	); err != nil {
		t.Fatalf("disable second busy timeout: %v", err)
	}

	tx, err := first.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			t.Errorf("Rollback() error = %v", err)
		}
	}()
	if _, err := tx.ExecContext(
		t.Context(),
		`UPDATE notification_preferences
		SET enabled = NOT enabled
		WHERE id = 1`,
	); err != nil {
		t.Fatalf("hold write lock: %v", err)
	}

	_, busyErr := second.db.ExecContext(
		t.Context(),
		`UPDATE notification_preferences
		SET enabled = NOT enabled
		WHERE id = 1`,
	)
	if busyErr == nil {
		t.Fatal("second write error = nil, want SQLite busy")
	}
	if !IsBusy(busyErr) {
		t.Fatalf("second write error = %v, want SQLite busy", busyErr)
	}
	return busyErr
}
