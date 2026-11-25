// Package protocol defines the product API compatibility handshake.
package protocol

import (
	"errors"
	"netgo/internal/model"
	"slices"
)

const Header = "X-NetGo-Protocol"

var ErrIncompatible = errors.New("protocol_incompatible")

type Health struct {
	Protocol     int      `json:"protocol"`
	SiteID       string   `json:"site_id"`
	Capabilities []string `json:"capabilities"`
	ServerUnix   int64    `json:"server_unix"`
}

func Capabilities() []string {
	return []string{"native-auth-v1", "device-signature-v1", "leases-v1", "profile-revision-v1", "admin-v1", "diagnostics-v1"}
}

func (h Health) Validate(siteID string) error {
	if h.SiteID != siteID {
		return errors.New("site_identity_mismatch")
	}
	if h.Protocol != model.Protocol {
		return ErrIncompatible
	}
	for _, capability := range Capabilities()[:4] {
		if !slices.Contains(h.Capabilities, capability) {
			return ErrIncompatible
		}
	}
	if h.ServerUnix < 946684800 || h.ServerUnix > 4102444800 {
		return errors.New("invalid_control_health")
	}
	return nil
}
