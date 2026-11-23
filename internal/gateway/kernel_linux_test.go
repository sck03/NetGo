//go:build linux

package gateway

import (
	"context"
	"encoding/json"
	"netgo/internal/model"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestKernelLeaseRenewalAndRevocation requires an explicitly isolated network namespace.
func TestKernelLeaseRenewalAndRevocation(t *testing.T) {
	if os.Getenv("NETGO_PRIVILEGED_TESTS") != "1" {
		t.Skip("run the compiled test under unshare --net with NETGO_PRIVILEGED_TESTS=1")
	}
	current, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	host, err := os.Readlink("/proc/1/ns/net")
	if err != nil || host == current || os.Geteuid() != 0 {
		t.Fatal("refusing kernel firewall test outside an isolated root network namespace")
	}
	ctx := context.Background()
	if output, err := exec.Command("nft", "add", "table", "inet", "netgo_unrelated_test").CombinedOutput(); err != nil {
		t.Fatalf("create unrelated marker: %s: %v", output, err)
	}
	gate := &NFT{Interface: "ngtest0", LANInterface: "nglan0", Overlay: "100.96.0.0/16"}
	lease := model.Lease{ID: "kernel-test", IP: "100.96.0.2", ExpiresAt: time.Now().Add(20 * time.Second)}
	if err := gate.Apply(ctx, []model.Lease{lease}); err != nil {
		t.Fatal(err)
	}
	lease.ExpiresAt = time.Now().Add(110 * time.Second)
	if err := gate.Apply(ctx, []model.Lease{lease}); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("nft", "-j", "list", "set", "inet", "netgo", "active4").Output()
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Objects []struct {
			Set *struct {
				Elements []struct {
					Element struct {
						Expires int64 `json:"expires"`
					} `json:"elem"`
				} `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(output, &listed); err != nil {
		t.Fatal(err)
	}
	var expires int64
	for _, object := range listed.Objects {
		if object.Set != nil && len(object.Set.Elements) == 1 {
			expires = object.Set.Elements[0].Element.Expires
		}
	}
	if expires < 100 {
		t.Errorf("kernel expiry was not renewed: %s", output)
	}
	if err := gate.Apply(ctx, nil); err != nil {
		t.Fatal(err)
	}
	output, err = exec.Command("nft", "-j", "list", "set", "inet", "netgo", "active4").Output()
	if err != nil {
		t.Fatal(err)
	}
	listed.Objects = nil
	if err := json.Unmarshal(output, &listed); err != nil {
		t.Fatal(err)
	}
	for _, object := range listed.Objects {
		if object.Set != nil && len(object.Set.Elements) != 0 {
			t.Errorf("revoked address retained in kernel timeout set: %s", output)
		}
	}
	if err := exec.Command("nft", "list", "table", "inet", "netgo_unrelated_test").Run(); err != nil {
		t.Fatal("unrelated firewall table was modified", err)
	}
}
