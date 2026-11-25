package client

import (
	"encoding/json"
	"errors"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"testing"
)

type loginVault map[string][]byte

func (v loginVault) Read(id string) ([]byte, error) {
	b, ok := v[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}
func (v loginVault) Write(id string, b []byte) error { v[id] = append([]byte(nil), b...); return nil }
func TestUnattendedRequiresCurrentOptIn(t *testing.T) {
	v := loginVault{}
	c := &Controller{Root: t.TempDir(), Vault: v, status: Status{Generation: 3}}
	p := site.Profile{ID: "site", Origin: "https://site.example", CA: "trust", Protocol: 2}
	if e := c.saveVerifiedLogin(3, p, "user", "secret", false); e != nil || c.UnattendedSite() != "" {
		t.Fatal("saved without opt-in", e)
	}
	c.remember = true
	if e := c.saveVerifiedLogin(2, p, "user", "secret", false); e != nil || c.UnattendedSite() != "" {
		t.Fatal("stale login saved", e)
	}
	if e := c.saveVerifiedLogin(3, p, "user", "secret", true); e != nil || c.UnattendedSite() != "site" {
		t.Fatal("opt-in lost", e)
	}
	var saved unattendedLogin
	if e := json.Unmarshal(v[unattendedKey("site")], &saved); e != nil || saved.Password != "secret" || !saved.Internet {
		t.Fatal("vault record", e)
	}
	marker, _ := os.ReadFile(filepath.Join(c.Root, "unattended.json"))
	if string(marker) != "site" {
		t.Fatal("secret in settings")
	}
	if e := c.DisableUnattended(); e != nil {
		t.Fatal(e)
	}
	if c.UnattendedSite() != "" || c.remember || string(v[unattendedKey("site")]) != "{}" {
		t.Fatal("disabled credentials survived")
	}
	if e := c.RestoreUnattended(); e == nil || e.Error() != "unattended_disabled" {
		t.Fatal("restored disabled credentials", e)
	}
}
func TestUnattendedTrustAndVaultBinding(t *testing.T) {
	v := loginVault{}
	c := &Controller{Root: t.TempDir(), Vault: v, remember: true, status: Status{Generation: 1}}
	p := site.Profile{ID: "site", Origin: "https://site.example", CA: "old", Protocol: 2}
	if e := c.saveVerifiedLogin(1, p, "user", "secret", false); e != nil {
		t.Fatal(e)
	}
	p.CA = "changed"
	b, _ := json.Marshal(p)
	if e := os.WriteFile(filepath.Join(c.Root, "site.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if e := c.RestoreUnattended(); e == nil || e.Error() != "unattended_trust_changed" {
		t.Fatal("changed trust resumed", e)
	}
	v[unattendedKey("site")] = []byte("{}")
	if e := c.RestoreUnattended(); e == nil || e.Error() != "unattended_credentials_required" {
		t.Fatal("invalid vault record resumed", e)
	}
	delete(v, unattendedKey("site"))
	if e := c.RestoreUnattended(); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
