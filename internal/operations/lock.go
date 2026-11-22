package operations

import (
	"errors"
	"os"
)

// Config changes and release switches stop the same services. Lock before
// reading their previous state so a queued transaction cannot restore stale data.
func lockOperation(configPath string) (func(), error) {
	path := configPath + ".operation-lock"
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, errors.New("another configuration/release operation or interrupted operation-lock exists")
	}
	return func() { _ = os.Remove(path) }, nil
}
