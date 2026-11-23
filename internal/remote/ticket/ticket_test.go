package ticket

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var updateVectors = flag.Bool("update-ticket-vectors", false, "regenerate the public cross-language test-only vectors")

// Public deterministic test keys. Never use this seed or these vectors in a host.
func fixture(t *testing.T) (ed25519.PrivateKey, Binding, Claims) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	key := ed25519.NewKeyFromSeed(seed)
	b := Binding{SiteID: "test-site", UserID: "test-user", ControllerID: "test-controller", HostID: "test-host"}
	copy(b.SiteKey[:], key.Public().(ed25519.PublicKey))
	for i := range b.ControllerKey {
		b.ControllerKey[i], b.HostKey[i] = byte(i+33), byte(i+65)
	}
	for i := range b.HostBoot {
		b.HostBoot[i] = byte(i + 97)
	}
	digest, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	c := Claims{Binding: digest, Generation: 4, PolicyRevision: 2, Sequence: 1, IssuedMS: 1700000000000,
		ExpiresMS: 1700000020000, Capabilities: CapabilityView | CapabilityInput, Consent: true}
	for i := range c.Session {
		c.Session[i] = byte(i + 113)
	}
	return key, b, c
}

func vectors(t *testing.T) map[string][]byte {
	t.Helper()
	key, b, c := fixture(t)
	values := map[string][]byte{"public_key": b.SiteKey[:], "binding": c.Binding[:]}
	sign := func(name string, claims Claims) {
		wire, err := Sign(key, claims)
		if err != nil {
			t.Fatal(name, err)
		}
		values[name] = wire
	}
	sign("begin", c)
	c.Sequence, c.IssuedMS, c.ExpiresMS, c.Capabilities = 2, c.IssuedMS+1000, c.ExpiresMS+1000, CapabilityView
	sign("renew", c)
	c.Sequence, c.Capabilities = 3, CapabilityView|CapabilityInput|CapabilityAudio
	sign("expand", c)
	c.Binding[0] ^= 1
	sign("foreign", c)
	c.Binding[0] ^= 1
	c.IssuedMS, c.ExpiresMS = 1699999960000, 1699999980000
	sign("expired", c)
	for name, change := range map[string]func([]byte){
		"invalid_caps":       func(b []byte) { binary.BigEndian.PutUint32(b[96:], 128|CapabilityView) },
		"invalid_consent":    func(b []byte) { binary.BigEndian.PutUint32(b[100:], 2) },
		"invalid_sequence":   func(b []byte) { binary.BigEndian.PutUint64(b[72:], 0) },
		"invalid_generation": func(b []byte) { binary.BigEndian.PutUint64(b[56:], 0) },
		"invalid_policy":     func(b []byte) { binary.BigEndian.PutUint64(b[64:], 0) },
		"invalid_no_view":    func(b []byte) { binary.BigEndian.PutUint32(b[96:], CapabilityInput) },
		"invalid_version":    func(b []byte) { b[7] = '2' },
		"invalid_lifetime":   func(b []byte) { binary.BigEndian.PutUint64(b[88:], 1700000030001) },
		"invalid_issued":     func(b []byte) { binary.BigEndian.PutUint64(b[80:], 0) },
		"invalid_order":      func(b []byte) { binary.BigEndian.PutUint64(b[88:], 1700000000000) },
		"invalid_overflow": func(b []byte) {
			binary.BigEndian.PutUint64(b[80:], ^uint64(0)-1)
			binary.BigEndian.PutUint64(b[88:], 1)
		},
		"invalid_session": func(b []byte) { clear(b[40:56]) },
		"invalid_binding": func(b []byte) { clear(b[8:40]) },
	} {
		wire := bytes.Clone(values["begin"])
		change(wire)
		copy(wire[MessageSize:], ed25519.Sign(key, wire[:MessageSize]))
		values[name] = wire
	}
	return values
}

func TestCrossLanguageVectors(t *testing.T) {
	values := vectors(t)
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	var expected strings.Builder
	for _, name := range names {
		expected.WriteString(name + " " + hex.EncodeToString(values[name]) + "\n")
	}
	path := filepath.Join("..", "..", "..", "tests", "contracts", "remote", "tickets.txt")
	if *updateVectors {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(expected.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != expected.String() {
		t.Fatal("cross-language vectors drifted; review format before explicit regeneration", err)
	}
	for name, wire := range values {
		if name == "binding" || name == "public_key" {
			continue
		}
		claims, err := Verify(values["public_key"], wire)
		invalid := strings.HasPrefix(name, "invalid_")
		if (err != nil) != invalid || invalid && claims != (Claims{}) {
			t.Fatal("unexpected verification for", name, err)
		}
	}
}

func TestSignedTicketRejectsEveryByteMutationAndLength(t *testing.T) {
	key, _, c := fixture(t)
	wire, err := Sign(key, c)
	if err != nil {
		t.Fatal(err)
	}
	public := key.Public().(ed25519.PublicKey)
	got, err := Verify(public, wire)
	if err != nil || got != c {
		t.Fatal("claim roundtrip", err)
	}
	reject := func(value []byte) {
		t.Helper()
		if claims, err := Verify(public, value); err == nil || claims != (Claims{}) {
			t.Fatal("accepted altered/truncated/trailing ticket or leaked claims")
		}
	}
	for i := range wire {
		changed := bytes.Clone(wire)
		changed[i] ^= 1
		reject(changed)
		reject(wire[:i])
	}
	reject(append(bytes.Clone(wire), 0))
	for _, wrong := range []ed25519.PublicKey{nil, public[:31], bytes.Repeat([]byte{2}, 32), make([]byte, 32)} {
		if _, err := Verify(wrong, wire); err == nil {
			t.Fatal("wrong public key accepted")
		}
	}
	if _, err := Sign(nil, c); err == nil {
		t.Fatal("invalid signing key accepted")
	}
	for _, invalid := range []Claims{{}, {Binding: c.Binding, Session: c.Session, Generation: 1, PolicyRevision: 1, Sequence: 1,
		IssuedMS: ^uint64(0) - 1, ExpiresMS: 1, Capabilities: CapabilityView}} {
		if _, err := Sign(key, invalid); err == nil {
			t.Fatal("invalid claims signed")
		}
	}
}

func TestBindingFencesEveryIdentityAndUsesUnambiguousEncoding(t *testing.T) {
	_, b, c := fixture(t)
	changes := []func(*Binding){func(v *Binding) { v.SiteID += "2" }, func(v *Binding) { v.UserID += "2" },
		func(v *Binding) { v.ControllerID += "2" }, func(v *Binding) { v.HostID += "2" },
		func(v *Binding) { v.SiteKey[0] ^= 1 }, func(v *Binding) { v.ControllerKey[0] ^= 1 },
		func(v *Binding) { v.HostKey[0] ^= 1 }, func(v *Binding) { v.HostBoot[0] ^= 1 }}
	for _, change := range changes {
		changed := b
		change(&changed)
		digest, err := changed.Digest()
		if err != nil || digest == c.Binding {
			t.Fatal("identity not bound", err)
		}
	}
	for _, invalid := range []string{"", "with space", "x\x00y", "测试", strings.Repeat("x", 129)} {
		changed := b
		changed.SiteID = invalid
		if _, err := changed.Digest(); err == nil {
			t.Fatal("invalid canonical ID accepted")
		}
	}
	b.UserID, b.ControllerID = "ab", "c"
	one, _ := b.Digest()
	b.UserID, b.ControllerID = "a", "bc"
	two, _ := b.Digest()
	if one == two {
		t.Fatal("binding field boundaries ambiguous")
	}
}

func FuzzVerifyNeverPanics(f *testing.F) {
	f.Add([]byte("NGRMTK01"))
	f.Add(make([]byte, Size))
	f.Fuzz(func(t *testing.T, wire []byte) {
		claims, err := Verify(bytes.Repeat([]byte{1}, 32), wire)
		if err != nil && claims != (Claims{}) {
			t.Fatal("failed verification exposed claims")
		}
	})
}
