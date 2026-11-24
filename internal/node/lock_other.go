//go:build !linux

package node

import "errors"

func lock(string) (func(), error) { return nil, errors.New("node maintenance requires Linux") }
