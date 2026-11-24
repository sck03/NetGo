package main

import (
	"fmt"
	"netgo/internal/node"
	"os"
)

func main() {
	if err := node.Main("vps", os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
