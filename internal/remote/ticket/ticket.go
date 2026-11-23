// Package ticket defines authenticated remote-session grants, independently of
// HTTP, storage, the VPN engine and platform media. It does not authorize peers
// or implement replay consumption; the host coordinator must do both.
package ticket

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

const (
	MessageSize = 104
	Size        = MessageSize + ed25519.SignatureSize
	MaxLeaseMS  = 30_000
)

const (
	CapabilityView      uint32 = 1
	CapabilityInput     uint32 = 2
	CapabilityAudio     uint32 = 4
	CapabilityClipboard uint32 = 8
	CapabilityUpload    uint32 = 16
	CapabilityDownload  uint32 = 32
	AllCapabilities            = uint32(63)
)

var ErrInvalid = errors.New("invalid_remote_ticket")

var magic = [8]byte{'N', 'G', 'R', 'M', 'T', 'K', '0', '1'}

type Claims struct {
	Binding        [32]byte
	Session        [16]byte
	Generation     uint64
	PolicyRevision uint64
	Sequence       uint64
	IssuedMS       uint64
	ExpiresMS      uint64
	Capabilities   uint32
	Consent        bool
}

func (c Claims) valid() bool {
	return c.Binding != [32]byte{} && c.Session != [16]byte{} &&
		c.Generation != 0 && c.PolicyRevision != 0 && c.Sequence != 0 && c.IssuedMS != 0 &&
		c.ExpiresMS > c.IssuedMS && c.ExpiresMS-c.IssuedMS <= MaxLeaseMS &&
		c.Capabilities&CapabilityView != 0 && c.Capabilities & ^AllCapabilities == 0
}

// Sign produces exactly one canonical message; it never serializes Go structs.
func Sign(key ed25519.PrivateKey, c Claims) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize || !c.valid() {
		return nil, ErrInvalid
	}
	out := make([]byte, Size)
	copy(out[:8], magic[:])
	copy(out[8:40], c.Binding[:])
	copy(out[40:56], c.Session[:])
	for i, value := range []uint64{c.Generation, c.PolicyRevision, c.Sequence, c.IssuedMS, c.ExpiresMS} {
		binary.BigEndian.PutUint64(out[56+i*8:], value)
	}
	binary.BigEndian.PutUint32(out[96:], c.Capabilities)
	if c.Consent {
		binary.BigEndian.PutUint32(out[100:], 1)
	}
	copy(out[MessageSize:], ed25519.Sign(key, out[:MessageSize]))
	return out, nil
}

// Verify authenticates structure and signature. The caller must independently
// check binding, both clocks, current policy and persistent one-time consumption.
func Verify(key ed25519.PublicKey, wire []byte) (Claims, error) {
	var c Claims
	if len(key) != ed25519.PublicKeySize || bytes.Equal(key, make([]byte, ed25519.PublicKeySize)) ||
		len(wire) != Size || !bytes.Equal(wire[:8], magic[:]) ||
		!ed25519.Verify(key, wire[:MessageSize], wire[MessageSize:]) {
		return c, ErrInvalid
	}
	copy(c.Binding[:], wire[8:40])
	copy(c.Session[:], wire[40:56])
	c.Generation = binary.BigEndian.Uint64(wire[56:])
	c.PolicyRevision = binary.BigEndian.Uint64(wire[64:])
	c.Sequence = binary.BigEndian.Uint64(wire[72:])
	c.IssuedMS = binary.BigEndian.Uint64(wire[80:])
	c.ExpiresMS = binary.BigEndian.Uint64(wire[88:])
	c.Capabilities = binary.BigEndian.Uint32(wire[96:])
	consent := binary.BigEndian.Uint32(wire[100:])
	c.Consent = consent == 1
	if consent > 1 || !c.valid() {
		return Claims{}, ErrInvalid
	}
	return c, nil
}

// Binding uses authenticated canonical IDs and raw public keys, never aliases or
// addresses supplied by the remote endpoint. A new boot instance fences old grants.
type Binding struct {
	SiteID, UserID, ControllerID, HostID string
	SiteKey, ControllerKey, HostKey      [32]byte
	HostBoot                             [16]byte
}

func validID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

func (b Binding) Digest() ([32]byte, error) {
	var out [32]byte
	if !validID(b.SiteID) || !validID(b.UserID) || !validID(b.ControllerID) || !validID(b.HostID) ||
		b.SiteKey == [32]byte{} || b.ControllerKey == [32]byte{} || b.HostKey == [32]byte{} || b.HostBoot == [16]byte{} {
		return out, ErrInvalid
	}
	h := sha256.New()
	h.Write([]byte("NetGo remote binding v1\x00"))
	for _, field := range [][]byte{[]byte(b.SiteID), b.SiteKey[:], []byte(b.UserID), []byte(b.ControllerID), b.ControllerKey[:], []byte(b.HostID), b.HostKey[:], b.HostBoot[:]} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		h.Write(size[:])
		h.Write(field)
	}
	copy(out[:], h.Sum(nil))
	return out, nil
}
