package ddns

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRouterAddressFileIsAuthoritativeAndMustBeFresh(t *testing.T) {
	c := fixture()
	c.Witnesses = nil
	c.AddressFile = filepath.Join(t.TempDir(), "wan2.ip")
	c.AddressMaxAgeSeconds = 60
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.AddressFile, []byte("8.8.8.8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := c.fileAddress()
	if err != nil || a.String() != "8.8.8.8" {
		t.Fatal("router WAN address rejected", a, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(c.AddressFile, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := c.fileAddress(); err == nil {
		t.Fatal("stale WAN address accepted")
	}
	for _, value := range []string{"192.168.1.1", "100.64.0.1", "8.8.8.8\n1.1.1.1", "2001:4860:4860::8888"} {
		if err := os.WriteFile(c.AddressFile, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.fileAddress(); err == nil {
			t.Fatal("invalid WAN address accepted", value)
		}
	}
	c.Witnesses = fixture().Witnesses
	if c.Validate() == nil {
		t.Fatal("ambiguous router/witness address sources accepted")
	}
}
