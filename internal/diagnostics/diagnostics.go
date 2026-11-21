package diagnostics

import (
	"encoding/json"
	"netgo/internal/model"
	"netgo/internal/site"
	"netgo/internal/store"
	"os"
	"runtime"
	"time"
)

func Report(c site.Config, s *store.Store) ([]byte, error) {
	st, e := s.View()
	if e != nil {
		return nil, e
	}
	return json.MarshalIndent(map[string]any{"generated_at": time.Now().UTC(), "version": model.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "site_id": c.Site.ID, "counts": map[string]int{"users": len(st.Users), "devices": len(st.Devices), "leases": len(st.Leases), "resources": len(st.Resources)}, "revision": st.Revision, "observed_revision": st.ObservedRevision, "reconciliation_error": st.LastError, "network_acceptance": "NOT-RUN"}, "", "  ")
}
func Write(path string, b []byte) error { return os.WriteFile(path, b, 0600) }
