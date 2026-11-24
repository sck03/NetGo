package site

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// AccountPrefix groups local device identities without exposing account names.
func AccountPrefix(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "acct-" + hex.EncodeToString(sum[:8]) + "-"
}

// AccountScope separates WireGuard profiles and device keys by site and login.
// Its normalization matches the identity service's account lookup.
func AccountScope(id, alias string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(alias))))
	return AccountPrefix(id) + hex.EncodeToString(sum[:20])
}
