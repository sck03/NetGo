//go:build linux

package node

import (
	"strings"
	"testing"
)

func TestProductionSystemdPathAcceptedByOwnershipCheck(t *testing.T) {
	m := Manager{Role: "server"}
	state := Installation{Owner: Owner, Role: "server", Files: map[string]string{"/etc/systemd/system/netgo-ownership-regression-test.service": "missing"}}
	err := m.verifyInstalled(state)
	if err == nil || !strings.Contains(err.Error(), "受管文件被外部修改") {
		t.Fatalf("standard systemd path rejected before file verification: %v", err)
	}
	state.Files = map[string]string{"/etc/systemd/system-other/netgo.service": "missing"}
	err = m.verifyInstalled(state)
	if err == nil || err.Error() != "非法归属路径" {
		t.Fatalf("outside systemd directory accepted: %v", err)
	}
}
