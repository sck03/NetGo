// Package platform keeps Linux package architecture checks independent of
// installation, service management and release transactions.
package platform

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

func Supported(arch string) bool { return arch == "amd64" || arch == "arm64" }

func VerifyELF(path, arch string) error {
	machine := uint16(62)
	if arch == "arm64" {
		machine = 183
	} else if arch != "amd64" {
		return fmt.Errorf("unsupported Linux architecture: %s", arch)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var header [64]byte
	if _, err = io.ReadFull(file, header[:]); err != nil {
		return fmt.Errorf("invalid Linux executable: %w", err)
	}
	kind := binary.LittleEndian.Uint16(header[16:18])
	if string(header[:7]) != "\x7fELF\x02\x01\x01" || (kind != 2 && kind != 3) || binary.LittleEndian.Uint16(header[18:20]) != machine {
		return fmt.Errorf("expected Linux ELF64 %s executable: %s", arch, path)
	}
	return nil
}
