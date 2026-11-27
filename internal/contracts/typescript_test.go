package contracts

import (
	"os"
	"testing"
)

func TestUIModelsMatchServer(t *testing.T) {
	actual, err := os.ReadFile("../../packages/ui/models.generated.ts")
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != TypeScript() {
		t.Fatal("UI models differ from Go JSON DTOs; run go generate ./internal/contracts")
	}
}
