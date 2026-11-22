package diagnostics

import (
	"context"
	"encoding/json"
	"netgo/internal/model"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"netgo/internal/store"
	"runtime"
	"time"
)

func Report(c site.Config, s *store.Store) ([]byte, error) {
	st, e := s.View()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	monitor := NewMonitor()
	monitor.Sample(ctx, c)
	return json.MarshalIndent(map[string]any{"generated_at": time.Now().UTC(), "version": model.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "site_id": c.Site.ID, "config_revision": c.Site.ConfigRevision, "counts": map[string]int{"users": len(st.Users), "devices": len(st.Devices), "leases": len(st.Leases), "resources": len(st.Resources)}, "revision": st.Revision, "observed_revision": st.ObservedRevision, "reconciliation_failed": st.LastError != "", "runtime": monitor.Snapshot(c, st), "process_scope": "diagnostic collector; use admin diagnostics for server process metrics", "network_acceptance": "NOT-RUN"}, "", "  ")
}
func Write(path string, b []byte) error { return securefile.Create(path, b, 0600) }
