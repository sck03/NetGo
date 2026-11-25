package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"os"
	"path/filepath"
)

// Unattended access is opt-in per user and site. A fresh authenticated login is
// performed after restart; transport tickets and VPN leases are never restored.
type unattendedLogin struct {
	Site     string `json:"site"`
	Alias    string `json:"alias"`
	Password string `json:"password"`
	Profile  string `json:"profile"`
	Internet bool   `json:"internet"`
}

func unattendedKey(id string) string {
	sum := sha256.Sum256([]byte("remote-login/" + id))
	return hex.EncodeToString(sum[:])
}
func profileIdentity(p site.Profile) string {
	// Public connection discovery can change. Trust-anchor changes must require
	// a new local login before unattended access can resume.
	b, _ := json.Marshal(struct{ ID, Origin, CA string }{p.ID, p.Origin, p.CA})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (c *Controller) saveVerifiedLogin(gen uint64, p site.Profile, alias, password string, internet bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.Generation != gen || !c.remember {
		return nil
	}
	if c.Vault == nil || alias == "" || password == "" {
		return errors.New("unattended_credentials_required")
	}
	b, e := json.Marshal(unattendedLogin{p.ID, alias, password, profileIdentity(p), internet})
	if e != nil {
		return e
	}
	defer clear(b)
	if e = c.Vault.Write(unattendedKey(p.ID), b); e != nil {
		return e
	}
	return securefile.Write(filepath.Join(c.Root, "unattended.json"), []byte(p.ID), 0600)
}
func (c *Controller) UnattendedSite() string {
	b, e := os.ReadFile(filepath.Join(c.Root, "unattended.json"))
	if e != nil || !site.ValidID(string(b)) {
		return ""
	}
	return string(b)
}
func (c *Controller) DisableUnattended() error {
	c.op.Lock()
	defer c.op.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remember = false
	id := c.UnattendedSite()
	if id == "" {
		return nil
	}
	// A tombstone also works with platform vaults which do not expose deletion.
	if e := c.Vault.Write(unattendedKey(id), []byte("{}")); e != nil {
		return e
	}
	e := os.Remove(filepath.Join(c.Root, "unattended.json"))
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (c *Controller) RestoreUnattended() error {
	c.op.Lock()
	defer c.op.Unlock()
	id := c.UnattendedSite()
	if id == "" {
		return errors.New("unattended_disabled")
	}
	data, e := c.Vault.Read(unattendedKey(id))
	if e != nil {
		return e
	}
	defer clear(data)
	var login unattendedLogin
	if json.Unmarshal(data, &login) != nil || login.Site != id || login.Alias == "" || login.Password == "" {
		return errors.New("unattended_credentials_required")
	}
	b, e := os.ReadFile(filepath.Join(c.Root, id+".json"))
	if e != nil {
		return e
	}
	var p site.Profile
	if json.Unmarshal(b, &p) != nil || p.Validate() != nil || profileIdentity(p) != login.Profile {
		return errors.New("unattended_trust_changed")
	}
	status := c.Status()
	if status.State != "disconnected" && status.State != "failed" {
		return errors.New("unattended_connection_active")
	}
	return c.connectLocked(id, login.Alias, login.Password, false, login.Internet, true)
}
