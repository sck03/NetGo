package netbird

import (
	"context"
	"log/slog"
	"time"
)

// Run repairs out-of-band drift independently from expiry sweeps. Revisions
// trigger early work; unchanged policy is verified once per minute. Failed
// upstream requests back off without holding up local revocation.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var nextVerify, retryAt time.Time
	backoff := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if now.Before(retryAt) {
				continue
			}
			state, err := r.Store.Revision()
			if err == nil && state.Desired == state.Observed && now.Before(nextVerify) {
				continue
			}
			if err == nil {
				apply, cancel := context.WithTimeout(ctx, 20*time.Second)
				err = r.Apply(apply)
				cancel()
			}
			if err != nil {
				slog.Warn("network reconciliation pending")
				retryAt = time.Now().Add(backoff)
				backoff = min(backoff*2, time.Minute)
			} else {
				backoff = 5 * time.Second
				retryAt = time.Time{}
				nextVerify = time.Now().Add(time.Minute)
			}
		}
	}
}
