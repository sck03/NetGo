package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerationSurvivesRestartAndRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	first, err := nextGeneration(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	next, err := nextGeneration(root, 0)
	if err != nil || next <= first {
		t.Fatal("restart reused old generation", next, err)
	}
	_ = os.WriteFile(filepath.Join(root, ".generation"), []byte("18446744073709551615"), 0600)
	if _, err := nextGeneration(root, 0); err == nil {
		t.Fatal("generation wrapped")
	}
	_ = os.WriteFile(filepath.Join(root, ".generation"), []byte("1\n"), 0600)
	if _, err := nextGeneration(root, 0); err == nil {
		t.Fatal("corrupt generation accepted")
	}
}
