package model

// CheckEditToken fences both stale updates and deletion followed by recreation.
// Empty tokens mean create; tokens are replaced only when saved data changes.
func CheckEditToken(current, expected string, exists bool) error {
	if (exists && (current == "" || current != expected)) || (!exists && expected != "") {
		return ErrEditConflict
	}
	return nil
}
