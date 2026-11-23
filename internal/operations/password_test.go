package operations

import (
	"context"
	"encoding/json"
	"errors"
	"netgo/internal/model"
	"netgo/internal/site"
	"netgo/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type resetGate struct {
	fail  bool
	calls int
}

func (g *resetGate) Apply(_ context.Context, leases []model.Lease) error {
	g.calls++
	if len(leases) != 0 {
		return errors.New("password reset opened access")
	}
	if g.fail {
		return errors.New("gate unavailable")
	}
	return nil
}

func passwordFixture(t *testing.T) (string, *store.Store) {
	t.Helper()
	c := configuration()
	root := t.TempDir()
	c.Database = filepath.Join(root, "metadata.db")
	path := filepath.Join(root, "site.json")
	b, _ := json.Marshal(c)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(c.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.Update(func(st *model.State) error {
		st.Users["u"] = model.User{ID: "u", Email: "user@example.test"}
		st.Users["other"] = model.User{ID: "other"}
		st.Credentials["old-token-hash"] = model.Credential{UserID: "u", ExpiresAt: time.Now().Add(time.Hour)}
		st.Credentials["other-token-hash"] = model.Credential{UserID: "other", ExpiresAt: time.Now().Add(time.Hour)}
		st.Leases["lease"] = model.Lease{ID: "lease", UserID: "u"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestPasswordResetRevokesBeforeIdPAndResumesOriginalServiceState(t *testing.T) {
	path, db := passwordFixture(t)
	gate := &resetGate{}
	var commands []string
	activeCalls, changes := 0, 0
	transaction := PasswordTransaction{
		Run: func(_ context.Context, name string, args ...string) error {
			commands = append(commands, name+" "+strings.Join(args, " "))
			return nil
		},
		Active: func(_ context.Context, name string) (bool, error) { activeCalls++; return name != "netgo-router", nil },
		Gate:   gate,
		Health: func(context.Context, site.Config) error { return nil },
		Change: func(_ context.Context, cfg, email string, password []byte) error {
			changes++
			st, err := db.View()
			if err != nil {
				return err
			}
			if _, ok := st.Credentials["old-token-hash"]; ok || len(st.Leases) != 0 || gate.calls == 0 {
				t.Fatal("IdP invoked before revocation")
			}
			if _, ok := st.Credentials["other-token-hash"]; !ok {
				t.Fatal("unrelated credential deleted")
			}
			if cfg != path || email != "user@example.test" || string(password) != "New-Pass-123!" {
				t.Fatal("wrong password reset input")
			}
			if changes == 1 {
				return errors.New("IdP interrupted")
			}
			return nil
		},
	}
	if err := transaction.Reset(context.Background(), path, "u", []byte("New-Pass-123!")); err == nil {
		t.Fatal("failure hidden")
	}
	for _, cmd := range commands {
		if strings.Contains(cmd, " start ") {
			t.Fatal("service started after incomplete reset")
		}
	}
	b, err := os.ReadFile(path + ".password-reset.json")
	if err != nil || strings.Contains(string(b), "New-Pass") {
		t.Fatalf("unsafe/missing recovery journal: %v", err)
	}
	if err = transaction.Reset(context.Background(), path, "u", []byte("New-Pass-123!")); err != nil {
		t.Fatal(err)
	}
	if activeCalls != 3 || changes != 2 {
		t.Fatal("original service state lost on retry")
	}
	if strings.Contains(strings.Join(commands, "\n"), "start netgo-router") {
		t.Fatal("inactive router was started")
	}
	if _, err = os.Stat(path + ".password-reset.json"); !os.IsNotExist(err) {
		t.Fatal("completed reset kept journal")
	}
	st, err := db.View()
	if err != nil || st.Users["u"].AuthRevision != 2 {
		t.Fatal("authentication revision not fenced", err)
	}
}

func TestPasswordResetStopsBeforeChangeOnGateFailureOrInvalidInput(t *testing.T) {
	path, _ := passwordFixture(t)
	calls := 0
	tx := PasswordTransaction{Run: func(context.Context, string, ...string) error { return nil }, Active: func(context.Context, string) (bool, error) { return false, nil }, Gate: &resetGate{fail: true}, Change: func(context.Context, string, string, []byte) error { calls++; return nil }}
	if err := tx.Reset(context.Background(), path, "u", []byte("New-Pass-123!")); err == nil || calls != 0 {
		t.Fatal("failed gate allowed password mutation")
	}
	if err := tx.Reset(context.Background(), path, "other", []byte("New-Pass-123!")); err == nil || calls != 0 {
		t.Fatal("another user overwrote recovery journal")
	}
	for _, value := range []string{"short", "with\nnewlines", strings.Repeat("x", 73)} {
		if err := tx.Reset(context.Background(), path, "u", []byte(value)); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
}

func TestInvalidPasswordLeavesServicesAndCredentialsUntouched(t *testing.T) {
	path, db := passwordFixture(t)
	unexpected := func() { t.Fatal("invalid password reached maintenance side effects") }
	tx := PasswordTransaction{
		Run:    func(context.Context, string, ...string) error { unexpected(); return nil },
		Active: func(context.Context, string) (bool, error) { unexpected(); return false, nil },
		Change: func(context.Context, string, string, []byte) error { unexpected(); return nil },
	}
	for _, password := range []string{"short", "lowercase1!", "NoDigits!", "NoSpecial123", "New-Pass-123!\n", "New-Pass-123!\x00", "New-Pass-123!\xff", strings.Repeat("A", 71) + "1!"} {
		if err := tx.Reset(context.Background(), path, "u", []byte(password)); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	if _, err := os.Stat(path + ".password-reset.json"); !os.IsNotExist(err) {
		t.Fatal("invalid password created a maintenance journal", err)
	}
	st, err := db.View()
	if err != nil || len(st.Credentials) != 2 || len(st.Leases) != 1 || st.Users["u"].AuthRevision != 0 {
		t.Fatal("invalid password changed active credentials or leases", err)
	}
	for _, password := range []string{"New-Pass-123!", "\u00c9clair\u0661!", strings.Repeat("A", 70) + "1!"} {
		if err := validateResetPassword([]byte(password)); err != nil {
			t.Fatal("valid IdP password rejected", err)
		}
	}
}
