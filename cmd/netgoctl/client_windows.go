package main

import "errors"

func clientCommand(_, _, _, _, _ string, _ bool) error {
	return errors.New("use the native Windows desktop client")
}
