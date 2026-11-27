//go:build ignore

package main

import (
	"log"
	"netgo/internal/contracts"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: generate-contracts <output.ts>")
	}
	if err := os.WriteFile(os.Args[1], []byte(contracts.TypeScript()), 0644); err != nil {
		log.Fatal(err)
	}
}
