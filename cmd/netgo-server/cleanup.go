package main

import (
	"context"
	"log/slog"
	"time"
)

type cleanupService interface{ Retry(context.Context) error }

// Upstream deletion cannot delay local lease expiry or remote authorization.
func runCleanup(ctx context.Context, services ...cleanupService) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		for _, service := range services {
			if ctx.Err() != nil {
				return
			}
			attempt, cancel := context.WithTimeout(ctx, 8*time.Second)
			if err := service.Retry(attempt); err != nil && ctx.Err() == nil {
				slog.Warn("upstream identity cleanup pending")
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
