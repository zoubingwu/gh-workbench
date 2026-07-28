package syncer

import "time"

const (
	hotInterval     = 10 * time.Second
	recentCap       = 5 * time.Minute
	weekCap         = 30 * time.Minute
	oldCap          = 24 * time.Hour
	failureCap      = time.Hour
	recentActivity  = 24 * time.Hour
	weeklyActivity  = 7 * 24 * time.Hour
	defaultInterval = hotInterval
)

type Outcome uint8

const (
	OutcomeChanged Outcome = iota
	OutcomeUnchanged
	OutcomeFailed
)

func NextInterval(now, updatedAt time.Time, current time.Duration, outcome Outcome) time.Duration {
	if outcome == OutcomeChanged {
		return hotInterval
	}
	if current <= 0 {
		current = defaultInterval
	}

	next := min(current*2, capFor(now, updatedAt))
	if outcome == OutcomeFailed {
		return min(current*2, failureCap)
	}
	return next
}

func capFor(now, updatedAt time.Time) time.Duration {
	age := now.Sub(updatedAt)
	switch {
	case age <= recentActivity:
		return recentCap
	case age <= weeklyActivity:
		return weekCap
	default:
		return oldCap
	}
}
