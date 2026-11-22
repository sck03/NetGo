package site

import (
	"errors"
	"reflect"
)

// JSON revisions must remain exact in both native clients and browser tooling.
const MaxConfigRevision uint64 = 1<<53 - 1

// CheckProfileUpdate never accepts a new trust root under an existing identity.
// Revision zero reads old development exports; once versioned, it cannot return
// to zero. Rollback republishes earlier content at a higher revision.
func CheckProfileUpdate(old, next Profile) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if old.ID != next.ID || old.Origin != next.Origin || old.CA != next.CA || old.Protocol != next.Protocol {
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
