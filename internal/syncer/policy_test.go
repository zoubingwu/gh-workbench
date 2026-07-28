package syncer

import (
	"testing"
	"time"
)

func TestNextInterval(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		current   time.Duration
		updatedAt time.Time
		outcome   Outcome
		want      time.Duration
	}{
		{
			name:      "change returns to hot interval",
			current:   30 * time.Minute,
			updatedAt: now.Add(-30 * 24 * time.Hour),
			outcome:   OutcomeChanged,
			want:      10 * time.Second,
		},
		{
			name:      "recent unchanged doubles",
			current:   10 * time.Second,
			updatedAt: now.Add(-time.Hour),
			outcome:   OutcomeUnchanged,
			want:      20 * time.Second,
		},
		{
			name:      "recent item caps at five minutes",
			current:   4 * time.Minute,
			updatedAt: now.Add(-time.Hour),
			outcome:   OutcomeUnchanged,
			want:      5 * time.Minute,
		},
		{
			name:      "week old item caps at thirty minutes",
			current:   20 * time.Minute,
			updatedAt: now.Add(-3 * 24 * time.Hour),
			outcome:   OutcomeUnchanged,
			want:      30 * time.Minute,
		},
		{
			name:      "old open item can cool to one day",
			current:   16 * time.Hour,
			updatedAt: now.Add(-30 * 24 * time.Hour),
			outcome:   OutcomeUnchanged,
			want:      24 * time.Hour,
		},
		{
			name:      "failure backs off",
			current:   20 * time.Second,
			updatedAt: now.Add(-time.Hour),
			outcome:   OutcomeFailed,
			want:      40 * time.Second,
		},
		{
			name:      "failure caps at one hour",
			current:   45 * time.Minute,
			updatedAt: now.Add(-time.Hour),
			outcome:   OutcomeFailed,
			want:      time.Hour,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := NextInterval(now, test.updatedAt, test.current, test.outcome)
			if got != test.want {
				t.Fatalf("NextInterval() = %s, want %s", got, test.want)
			}
		})
	}
}
