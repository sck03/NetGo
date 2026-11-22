package site

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestSharedProfileUpdateContract(t *testing.T) {
	b, err := os.ReadFile("../../tests/contracts/profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Base  map[string]json.RawMessage
		Cases []struct {
			Name      string
			Old, Next map[string]json.RawMessage
			Accept    bool
		}
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			decode := func(patch map[string]json.RawMessage) (Profile, error) {
				values := map[string]json.RawMessage{}
				for k, v := range fixture.Base {
					values[k] = v
				}
				for k, v := range patch {
					values[k] = v
				}
				data, _ := json.Marshal(values)
				var p Profile
				err := Decode(bytes.NewReader(data), &p)
				return p, err
			}
			old, err := decode(tc.Old)
			if err != nil {
				t.Fatal(err)
			}
			next, err := decode(tc.Next)
			if err == nil {
				err = CheckProfileUpdate(old, next)
			}
			if (err == nil) != tc.Accept {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.Accept, err)
			}
		})
	}
}
