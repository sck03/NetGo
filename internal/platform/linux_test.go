package platform

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestELFArchitecture(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		header := make([]byte, 64)
		copy(header, "\x7fELF\x02\x01\x01")
		header[16] = 2
		machine := uint16(62)
		other := "arm64"
		if arch == "arm64" {
			machine = 183
			other = "amd64"
		}
		binary.LittleEndian.PutUint16(header[18:20], machine)
		path := filepath.Join(t.TempDir(), "program")
		if err := os.WriteFile(path, header, 0600); err != nil {
			t.Fatal(err)
		}
		if err := VerifyELF(path, arch); err != nil {
			t.Fatal(err)
		}
		if VerifyELF(path, other) == nil || VerifyELF(path, "arm") == nil {
			t.Fatal("wrong architecture accepted")
		}
		header[5] = 2
		os.WriteFile(path, header, 0600)
		if VerifyELF(path, arch) == nil {
			t.Fatal("big endian accepted")
		}
	}
}
