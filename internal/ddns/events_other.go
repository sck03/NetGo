//go:build !linux

package ddns

import (
	"context"
	"errors"
)

func NetworkEvents(context.Context) (<-chan struct{}, error) {
	return nil, errors.New("network event watcher requires Linux")
}
