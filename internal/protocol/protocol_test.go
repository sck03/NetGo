package protocol

import (
	"errors"
	"testing"
	"time"
)

func TestHealthRequiresAllNativeCapabilities(t *testing.T) {
	h := Health{Protocol: 2, SiteID: "site", Capabilities: Capabilities()}
	if err := h.Validate("site"); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		partial := h
		partial.Capabilities = append(append([]string{}, h.Capabilities[:i]...), h.Capabilities[i+1:]...)
		if err := partial.Validate("site"); !errors.Is(err, ErrIncompatible) {
			t.Fatalf("missing %s accepted", h.Capabilities[i])
		}
	}
	if err := h.Validate("different"); err == nil {
		t.Fatal("different identity accepted")
	}
	h.Protocol = 3
	if err := h.Validate("site"); !errors.Is(err, ErrIncompatible) {
		t.Fatal("future protocol accepted")
	}
}

func TestHealthAllowsClockSkewAfterTLSVerification(t *testing.T) {
	for _, offset := range []time.Duration{-24 * time.Hour, -time.Minute, 0, time.Minute, 24 * time.Hour} {
		h := Health{Protocol: 2, SiteID: "site", Capabilities: Capabilities(), ServerUnix: time.Now().Add(offset).Unix()}
		err := h.Validate("site")
		if err != nil {
			t.Fatal(err)
		}
	}
}
