package api

import (
	"netgo/internal/diagnostics"
	"netgo/internal/model"
	"netgo/internal/site"
)

// Response DTOs also generate the TypeScript contract used by the admin UI.
type AdminPageState struct {
	Users            map[string]model.User     `json:"users"`
	Resources        map[string]model.Resource `json:"resources"`
	Devices          map[string]model.Device   `json:"devices"`
	Leases           map[string]model.Lease    `json:"leases"`
	Revision         uint64                    `json:"revision"`
	ObservedRevision uint64                    `json:"observed_revision"`
	LastError        string                    `json:"last_error"`
	Runtime          diagnostics.RuntimeReport `json:"runtime"`
	Network          site.Access               `json:"network"`
	NextAfter        string                    `json:"next_after"`
}
type AdminSummary struct {
	Counts           map[string]int            `json:"counts"`
	Revision         uint64                    `json:"revision"`
	ObservedRevision uint64                    `json:"observed_revision"`
	LastError        string                    `json:"last_error"`
	Runtime          diagnostics.RuntimeReport `json:"runtime"`
}
