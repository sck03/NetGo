package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"netgo/internal/gateway"
	"netgo/internal/model"
	"netgo/internal/privileged"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"netgo/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unicode"
	"unicode/utf8"
)

type PasswordTransaction struct {
	Run    Command
	Active func(context.Context, string) (bool, error)
	Change func(context.Context, string, string, []byte) error
	Gate   gateway.Gate
	Health func(context.Context, site.Config) error
}

type passwordJournal struct {
	SiteID string          `json:"site_id"`
	UserID string          `json:"user_id"`
	Active map[string]bool `json:"active_services"`
	Phase  string          `json:"phase"`
	At     time.Time       `json:"started_at"`
}

var passwordServices = []string{"netgo-server", "netgo-router", "netbird-server"}

// Match the pinned NetBird IdP policy before stopping writers or revoking
// credentials. The IdP remains authoritative and validates again on mutation.
func validateResetPassword(password []byte) error {
	if len(password) < 8 || len(password) > 72 || !utf8.Valid(password) || bytes.ContainsAny(password, "\r\n\x00") {
		return errors.New("an 8..72 byte UTF-8 password without line breaks or NUL is required")
	}
	var digit, upper, special bool
	for _, c := range string(password) {
		switch {
		case unicode.IsDigit(c):
			digit = true
		case unicode.IsUpper(c):
			upper = true
		case !unicode.IsLetter(c):
			special = true
		}
	}
	if !digit || !upper || !special {
		return errors.New("password must contain a digit, an uppercase letter and a special character")
	}
	return nil
}

func serviceActive(ctx context.Context, service string) (bool, error) {
	err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", service).Run()
	if err == nil {
		return true, nil
	}
	var status *exec.ExitError
	if errors.As(err, &status) && (status.ExitCode() == 3 || status.ExitCode() == 4) {
		return false, nil
	}
	return false, fmt.Errorf("cannot determine %s service state", service)
}

func changePassword(ctx context.Context, configPath, email string, password []byte) error {
	// Use the pinned IdP owner's supported command, as its service account.
	// Neither password hashes nor IdP database writes belong to NetGo.
	command := exec.CommandContext(ctx, "runuser", "-u", "netgo", "--", "/opt/netgo/upstream/netbird-server",
		"--config", filepath.Join(filepath.Dir(configPath), "netbird.yaml"),
		"admin", "user", "change-password", "--email", email, "--password-file", "-")
	command.Stdin = bytes.NewReader(password)
	if err := command.Run(); err != nil {
		return errors.New("identity password update failed; services remain stopped; retry reset-password")
	}
	return nil
}

// Reset is a maintenance operation: all connections are interrupted. On failure
// it retains a secret-free journal and keeps writers stopped. Retrying the same
// user resumes with the original service states and never restores old tokens.
func (t PasswordTransaction) Reset(ctx context.Context, path, userID string, password []byte) error {
	if userID == "" {
		return errors.New("user ID is required")
	}
	if err := validateResetPassword(password); err != nil {
		return err
	}
	unlock, err := lockOperation(path)
	if err != nil {
		return err
	}
	defer unlock()
	c, err := site.Load(path)
	if err != nil {
		return err
	}
	st, err := store.Open(c.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	state, err := st.View()
	if err != nil {
		return err
	}
	user, ok := state.Users[userID]
	if !ok {
		return model.ErrNotFound
	}
	run, active, change, health := t.Run, t.Active, t.Change, t.Health
	if run == nil {
		run = Exec
	}
	if active == nil {
		active = serviceActive
	}
	if change == nil {
		change = changePassword
	}
	if health == nil {
		health = Health
	}
	gate := t.Gate
	if gate == nil {
		gate = &privileged.Client{Path: c.HelperSocket}
	}
	journalPath := path + ".password-reset.json"
	journal := passwordJournal{SiteID: c.Site.ID, UserID: userID, Active: map[string]bool{}, Phase: "prepared", At: time.Now().UTC()}
	if b, readErr := os.ReadFile(journalPath); readErr == nil {
		if err = site.Decode(bytes.NewReader(b), &journal); err != nil {
			return err
		}
		if journal.SiteID != c.Site.ID || journal.UserID != userID || len(journal.Active) != len(passwordServices) {
			return errors.New("password reset journal belongs to another operation; review it first")
		}
		for _, service := range passwordServices {
			if _, exists := journal.Active[service]; !exists {
				return errors.New("invalid password reset journal")
			}
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	} else {
		for _, service := range passwordServices {
			journal.Active[service], err = active(ctx, service)
			if err != nil {
				return err
			}
		}
		b, _ := json.MarshalIndent(journal, "", "  ")
		if err = securefile.Create(journalPath, b, 0600); err != nil {
			return err
		}
	}
	save := func(phase string) error {
		journal.Phase = phase
		b, _ := json.MarshalIndent(journal, "", "  ")
		return securefile.Write(journalPath, b, 0600)
	}
	if err = run(ctx, "systemctl", append([]string{"stop"}, passwordServices...)...); err != nil {
		return errors.New("password reset could not stop writers; journal retained")
	}
	if err = st.Update(func(state *model.State) error {
		if err := state.InvalidateUser(userID); err != nil {
			return err
		}
		state.Leases = map[string]model.Lease{}
		state.Operations = map[string]model.Operation{}
		state.Record("local", "user.password-reset.started", userID)
		return nil
	}); err != nil {
		return err
	}
	if err = gate.Apply(ctx, nil); err != nil {
		return errors.New("password reset could not close gateway leases; services remain stopped")
	}
	if err = save("access_revoked"); err != nil {
		return err
	}
	if err = change(ctx, path, user.Email, password); err != nil {
		return err
	}
	if err = save("password_updated"); err != nil {
		return err
	}
	for i := len(passwordServices) - 1; i >= 0; i-- {
		service := passwordServices[i]
		if !journal.Active[service] {
			continue
		}
		if err = run(ctx, "systemctl", "start", service); err != nil {
			return t.stopAfterPasswordFailure(run)
		}
	}
	if journal.Active["netgo-server"] {
		if err = waitHealth(ctx, health, c); err != nil {
			return t.stopAfterPasswordFailure(run)
		}
	}
	if err = st.Update(func(state *model.State) error {
		state.Record("local", "user.password-reset.completed", userID)
		return nil
	}); err != nil {
		return err
	}
	return os.Remove(journalPath)
}

func (PasswordTransaction) stopAfterPasswordFailure(run Command) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := run(ctx, "systemctl", append([]string{"stop"}, passwordServices...)...)
	return errors.Join(errors.New("password changed but service recovery failed; journal retained; retry reset-password"), err)
}
