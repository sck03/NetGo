package model

import (
	"encoding/json"
	"testing"
)

func TestDTOCollectionsAlwaysEncodeAsArrays(t *testing.T) {
	for _, tc := range []struct {
		value any
		key   string
	}{{Resource{}, "ports"}, {Lease{}, "resources"}, {User{}, "resources"}} {
		raw, err := json.Marshal(tc.value)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		if string(object[tc.key]) != "[]" {
			t.Fatalf("%s is not an array: %s", tc.key, raw)
		}
	}
}
