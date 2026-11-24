package site

import "testing"

func TestAccountScopeSeparatesSitesAndAccounts(t *testing.T) {
	a := AccountScope("site", "alice")
	if !ValidID(a) || a != AccountScope("site", " ALICE \t") {
		t.Fatal("scope does not match identity alias normalization", a)
	}
	if a == AccountScope("site", "bob") || a == AccountScope("another", "alice") {
		t.Fatal("different accounts or sites share a device identity")
	}
}
