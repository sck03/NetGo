//go:build !linux

package desktopinstall

import "errors"

func lockNetwork() (func(), error) { return nil, errors.New("Linux network installation only") }
