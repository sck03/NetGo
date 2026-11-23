// Package operations coordinates bounded, reviewable Linux configuration changes.
package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"netgo/internal/gateway"
	"netgo/internal/protocol"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"
)

type Command func(context.Context, string, ...string) error

func Exec(ctx context.Context, name string, args ...string) error {
	// Command output may contain private configuration. Report only the operation.
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

type ConfigPlan struct {
	SiteID                 string   `json:"site_id"`
	CurrentSiteRevision    uint64   `json:"current_site_revision"`
	NextSiteRevision       uint64   `json:"next_site_revision"`
	RecoverySiteRevision   uint64   `json:"recovery_site_revision"`
	Changed                []string `json:"changed_fields"`
	OwnedFiles             []string `json:"owned_files"`
	Services               []string `json:"restart_services"`
	GatewayRules           string   `json:"empty_lease_rules"`
	RequiresReconnect      bool     `json:"requires_reconnect"`
	RequiresTemplateRender bool     `json:"requires_template_render"`
}

func Plan(path string, old, next site.Config) (ConfigPlan, error) {
	var result ConfigPlan
	if err := next.Validate(); err != nil {
		return result, err
	}
	if err := site.CheckProfileUpdate(old.Site, next.Site); err != nil {
		return result, err
	}
	if !site.SameProfileContent(old.Site, next.Site) && next.Site.ConfigRevision <= old.Site.ConfigRevision {
		return result, errors.New("changed site configuration requires a higher ConfigRevision")
	}
	if next.Site.ConfigRevision > old.Site.ConfigRevision && next.Site.ConfigRevision >= site.MaxConfigRevision {
		return result, errors.New("reserve one configuration revision for automatic recovery")
	}
	// This transaction cannot migrate IdP identity, databases or secrets.
	if old.Site.ID != next.Site.ID || old.Site.Origin != next.Site.Origin || old.Site.CA != next.Site.CA ||
		old.Database != next.Database || old.ManagementURL != next.ManagementURL || old.ManagementTokenFile != next.ManagementTokenFile || old.NativeSecretFile != next.NativeSecretFile || old.HelperSocket != next.HelperSocket {
		return result, errors.New("identity/database/secret changes require a separate reviewed migration")
	}
	if old.Listen != next.Listen || old.Interface != next.Interface || old.LANInterface != next.LANInterface || old.OverlayCIDR != next.OverlayCIDR {
		return result, errors.New("listener/interface/overlay changes require a complete reviewed gateway and router deployment")
	}
	// TLS/frp/Relay files have separate ownership and must be rendered as a bundle.
	if !reflect.DeepEqual(old.Auxiliaries, next.Auxiliaries) || old.Deployment != next.Deployment || old.Site.LocalRelay != next.Site.LocalRelay || old.Site.AuxiliaryRelay != next.Site.AuxiliaryRelay {
		return result, errors.New("auxiliary/relay changes require render-deploy and component validation")
	}
	rules, err := gateway.Render(next.Interface, next.LANInterface, next.OverlayCIDR, nil, time.Now())
	if err != nil {
		return result, err
	}
	before, after := reflect.ValueOf(old), reflect.ValueOf(next)
	for i := 0; i < before.NumField(); i++ {
		if !reflect.DeepEqual(before.Field(i).Interface(), after.Field(i).Interface()) {
			result.Changed = append(result.Changed, before.Type().Field(i).Name)
		}
	}
	result.SiteID = next.Site.ID
	result.CurrentSiteRevision, result.NextSiteRevision = old.Site.ConfigRevision, next.Site.ConfigRevision
	result.RecoverySiteRevision = old.Site.ConfigRevision
	if next.Site.ConfigRevision > old.Site.ConfigRevision {
		result.RecoverySiteRevision = next.Site.ConfigRevision + 1
	}
	result.OwnedFiles = []string{path}
	result.Services = []string{"netgo-helper", "netgo-server"}
	result.GatewayRules = rules
	result.RequiresReconnect = len(result.Changed) > 0
	result.RequiresTemplateRender = false
	return result, nil
}

type ConfigTransaction struct {
	Run       Command
	Health    func(context.Context, site.Config) error
	Reconcile func(context.Context, site.Config) error
}

func Health(ctx context.Context, c site.Config) error {
	cl := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+c.Listen+"/netgo/v2/health", nil)
	if err != nil {
		return err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return errors.New("local control health unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("local control health failed")
	}
	var h protocol.Health
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4096)).Decode(&h); err != nil {
		return errors.New("invalid local control health")
	}
	return h.Validate(c.Site.ID)
}
func (t ConfigTransaction) Apply(ctx context.Context, path string, next site.Config) error {
	unlock, err := lockOperation(path)
	if err != nil {
		return err
	}
	defer unlock()
	old, err := site.Load(path)
	if err != nil {
		return err
	}
	plan, err := Plan(path, old, next)
	if err != nil {
		return err
	}
	if len(plan.Changed) == 0 {
		return nil
	}
	run := t.Run
	if run == nil {
		run = Exec
	}
	health := t.Health
	if health == nil {
		health = Health
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// An exclusive directory is a cross-process lock and retains recovery material
	// after power failure. Never silently overwrite an interrupted transaction.
	journal := path + ".transaction"
	if err = os.Mkdir(journal, 0700); err != nil {
		return errors.New("configuration transaction exists; review recovery material")
	}
	committed := false
	defer func() {
		if committed {
			_ = os.Remove(filepath.Join(journal, "previous.json"))
			_ = os.Remove(journal)
		}
	}()
	if err = securefile.Create(filepath.Join(journal, "previous.json"), previous, 0600); err != nil {
		return err
	}
	reconciled := false
	rollback := func(cause error) error {
		recoverCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		stopErr := run(recoverCtx, "systemctl", "stop", "netgo-server", "netgo-helper")
		restored := old
		recoveryData := previous
		if next.Site.ConfigRevision > old.Site.ConfigRevision {
			restored.Site.ConfigRevision = plan.RecoverySiteRevision
			recoveryData, _ = json.MarshalIndent(restored, "", "  ")
		}
		writeErr := securefile.Write(path, recoveryData, 0600)
		startErr := run(recoverCtx, "systemctl", "start", "netgo-helper", "netgo-server")
		checkErr := health(recoverCtx, restored)
		var policyErr error
		if reconciled && t.Reconcile != nil && checkErr == nil {
			policyErr = t.Reconcile(recoverCtx, restored)
		}
		if stopErr != nil || writeErr != nil || startErr != nil || checkErr != nil || policyErr != nil {
			return errors.Join(cause, errors.New("configuration rollback incomplete; retained .transaction journal"), stopErr, writeErr, startErr, checkErr, policyErr)
		}
		committed = true
		return fmt.Errorf("configuration change rolled back: %w", cause)
	}
	if err = run(ctx, "systemctl", "stop", "netgo-server", "netgo-helper"); err != nil {
		return rollback(err)
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return rollback(err)
	}
	if err = securefile.Write(path, data, 0600); err != nil {
		return rollback(err)
	}
	if err = run(ctx, "systemctl", "start", "netgo-helper", "netgo-server"); err != nil {
		return rollback(err)
	}
	if err = waitHealth(ctx, health, next); err != nil {
		return rollback(err)
	}
	if t.Reconcile != nil {
		reconciled = true
		if err = t.Reconcile(ctx, next); err != nil {
			return rollback(err)
		}
	}
	committed = true
	return nil
}
func waitHealth(ctx context.Context, check func(context.Context, site.Config) error, c site.Config) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = check(ctx, c); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return err
}
