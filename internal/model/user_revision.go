package model

import "reflect"

// AdvanceUserRevisions also covers dependency cleanup and password/device
// administration, so an old editor cannot restore removed access.
func AdvanceUserRevisions(before, after *State) error {
	for id, user := range after.Users {
		old, exists := before.Users[id]
		if !exists {
			continue
		}
		user.EditRevision = old.EditRevision
		if !reflect.DeepEqual(old, user) {
			if old.EditRevision == ^uint64(0) {
				return ErrEditConflict
			}
			user.EditRevision++
		}
		after.Users[id] = user
	}
	return nil
}
