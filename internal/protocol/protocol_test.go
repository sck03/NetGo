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

func TestHealthRejectsClockSkewBeforeAuthentication(t *testing.T) {
	for _, offset := range []time.Duration{-time.Minute, 0, time.Minute} {
		h := Health{Protocol: 2, SiteID: "site", Capabilities: Capabilities(), ServerUnix: time.Now().Add(offset).Unix()}
		err := h.Validate("site")
		if offset == 0 && err != nil {
			t.Fatal(err)
		}
		if offset != 0 && !errors.Is(err, ErrClockSkew) {
			t.Fatalf("clock offset %s was not diagnosed: %v", offset, err)
		}
	}
}
