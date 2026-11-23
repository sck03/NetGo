package netbird

import (
	"encoding/json"
	"testing"
)

func TestReconcileReadModelMatchesOnlyExactDesiredPolicy(t *testing.T) {
	var current object
	if err := json.Unmarshal([]byte(`{"id":"policy-id","name":"owned","enabled":true,"description":"managed","rules":[{"id":"read-only-id","name":"rule","enabled":true,"action":"accept","bidirectional":false,"protocol":"tcp","sources":[{"id":"user-group","peers_count":2}],"destinations":[{"id":"resource-group"}],"ports":["443","80"]}]}`), &current); err != nil {
		t.Fatal(err)
	}
	rule := map[string]any{"name": "rule", "enabled": true, "action": "accept", "bidirectional": false, "protocol": "tcp",
		"sources": []string{"user-group"}, "destinations": []string{"resource-group"}, "ports": []string{"80", "443"}}
	desired := map[string]any{"name": "owned", "enabled": true, "description": "managed", "rules": []any{rule}}
	if !matchesDesired(current, desired) {
		t.Fatal("unchanged expanded read model caused a policy rewrite")
	}
	rule["sources"] = []string{"different-user"}
	if matchesDesired(current, desired) {
		t.Fatal("changed membership was skipped")
	}
	rule["sources"] = []string{"user-group"}
	desired["enabled"] = false
	if matchesDesired(current, desired) {
		t.Fatal("revocation was skipped")
	}
	desired["enabled"] = true
	delete(current.Raw, "rules")
	if matchesDesired(current, desired) {
		t.Fatal("incomplete upstream read model trusted as unchanged")
	}
}

func TestReconcileGroupMembershipComparedIndependentOfOrder(t *testing.T) {
	var current object
	if err := json.Unmarshal([]byte(`{"id":"group","name":"owned","peers":[{"id":"b"},{"id":"a"}],"peers_count":2}`), &current); err != nil {
		t.Fatal(err)
	}
	if !matchesDesired(current, map[string]any{"name": "owned", "peers": []string{"a", "b"}}) {
		t.Fatal("membership order caused a rewrite")
	}
	if matchesDesired(current, map[string]any{"name": "owned", "peers": []string{}}) {
		t.Fatal("group clearing was skipped")
	}
	if matchesDesired(current, map[string]any{"name": "owned", "peers": []string{"a", "c"}}) {
		t.Fatal("same size but different peers were accepted")
	}
}
