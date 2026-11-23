package remote

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"netgo/internal/remote/ticket"
)

var ErrProof = errors.New("invalid_remote_proof")

// ProofMessage binds the server ticket to a fresh host challenge and the actual
// TLS connection. channel is a 32-byte exporter labeled EXPORTER-NetGo-remote-v1.
// A copied proof is invalid on another connection, ticket or host challenge.
func ProofMessage(wire, challenge, channel []byte) ([]byte, error) {
	if len(wire) != ticket.Size || len(challenge) != 32 || len(channel) != 32 {
		return nil, ErrProof
	}
	zero := [32]byte{}
	if [32]byte(challenge) == zero || [32]byte(channel) == zero {
		return nil, ErrProof
	}
	hash := sha256.Sum256(wire)
	out := append([]byte("NetGo remote possession v1\x00"), hash[:]...)
	out = append(out, challenge...)
	return append(out, channel...), nil
}

func verifyProof(key string, wire []byte, proof Possession) bool {
	pk, e := base64.RawURLEncoding.DecodeString(key)
	challenge, ce := base64.RawURLEncoding.DecodeString(proof.Challenge)
	channel, be := base64.RawURLEncoding.DecodeString(proof.Channel)
	sig, se := base64.RawURLEncoding.DecodeString(proof.Signature)
	msg, me := ProofMessage(wire, challenge, channel)
	return e == nil && ce == nil && be == nil && se == nil && me == nil && len(pk) == ed25519.PublicKeySize && ed25519.Verify(pk, msg, sig)
}

type Possession struct {
	Challenge string `json:"challenge"`
	Channel   string `json:"channel"`
	Signature string `json:"signature"`
}
