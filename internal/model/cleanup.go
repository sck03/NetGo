package model

import "time"

// Retry metadata survives restarts. Failed entries leave the head of the queue
// while other revoked identities continue to be removed.
type CleanupRetry struct {
	CleanupAttempts uint8     `json:"cleanup_attempts,omitempty"`
	CleanupAfter    time.Time `json:"cleanup_after"`
}

func (r *CleanupRetry) Failed(now time.Time) {
	if r.CleanupAttempts < 6 {
		r.CleanupAttempts++
	}
	r.CleanupAfter = now.Add(time.Minute * time.Duration(1<<(r.CleanupAttempts-1)))
}

type DeleteResult struct {
	Deleted        bool `json:"deleted"`
	CleanupPending bool `json:"cleanup_pending"`
}
