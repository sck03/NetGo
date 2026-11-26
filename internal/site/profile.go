package site

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
)

// JSON revisions must remain exact in both native clients and browser tooling.
const MaxConfigRevision uint64 = 1<<53 - 1

// TrustIdentity excludes discovery addresses, ports and display names. A
// versioned endpoint update must not invalidate credentials for the same trust
// root, while replacing the authority or CA always requires a fresh login.
func TrustIdentity(p Profile) string {
	u, _ := url.Parse(p.Origin)
	host := ""
	if u != nil {
		host = strings.ToLower(u.Hostname())
	}
	b, _ := json.Marshal([]any{p.Protocol, p.ID, host, p.CA})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// CheckProfileUpdate never accepts a new trust root under an existing identity.
// Revision zero is reserved for a locally created bootstrap profile. Once
// versioned, rollback republishes earlier content at a higher revision.
func CheckProfileUpdate(old, next Profile) error {
	if err := next.Validate(); err != nil {
		return err
	}
	a, _ := url.Parse(old.Origin)
	b, _ := url.Parse(next.Origin)
	if old.ID != next.ID || !strings.EqualFold(a.Hostname(), b.Hostname()) || old.CA != next.CA || old.Protocol != next.Protocol {
		return errors.New("site_trust_changed_use_new_id")
	}
	if next.ConfigRevision < old.ConfigRevision {
		return errors.New("profile_revision_stale")
	}
	if old.ConfigRevision != 0 && next.ConfigRevision == old.ConfigRevision && !SameProfileContent(old, next) {
		return errors.New("profile_revision_conflict")
	}
	return nil
}

func SameProfileContent(a, b Profile) bool {
	// A user may choose a local display name without changing server configuration.
	a.Name, b.Name = "", ""
	a.ConfigRevision, b.ConfigRevision = 0, 0
	return reflect.DeepEqual(a, b)
}
