package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/term"
	"io"
	"net"
	"net/http"
	"netgo/internal/backup"
	"netgo/internal/diagnostics"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/node"
	"netgo/internal/operations"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"netgo/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func output(v any) error { e := json.NewEncoder(os.Stdout); e.SetIndent("", "  "); return e.Encode(v) }
func password() ([]byte, error) {
	fmt.Fprint(os.Stderr, "备份密码：")
	p, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return p, e
}
func run() error {
	name := filepath.Base(os.Args[0])
	if name == "ng" || name == "netgo-serverctl" {
		return node.Main("server", os.Args[1:])
	}
	if len(os.Args) > 1 && os.Args[1] == "server" {
		return node.Main("server", os.Args[2:])
	}
	if len(os.Args) < 2 {
		return errors.New("commands: init, init-admin, reset-password, doctor, client import|connect|disconnect|status, config validate|plan|apply, export-site, reconcile, auxiliary add|remove, diagnostics, backup, restore, upgrade, rollback, version")
	}
	cmd := os.Args[1]
	known := map[string]bool{"version": true, "init": true, "init-admin": true, "reset-password": true, "doctor": true, "client": true, "config": true, "release": true, "export-site": true, "reconcile": true, "auxiliary": true, "diagnostics": true, "backup": true, "restore": true, "upgrade": true, "rollback": true}
	if !known[cmd] {
		return errors.New("unknown command")
	}
	if cmd == "version" {
		return output(map[string]any{"version": model.Version, "protocol": model.Protocol, "schema": model.Schema, "go": runtime.Version()})
	}
	sub := ""
	start := 2
	if cmd == "config" || cmd == "auxiliary" || cmd == "client" || cmd == "release" {
		if len(os.Args) < 3 {
			return errors.New("subcommand required")
		}
		sub = os.Args[2]
		start = 3
	}
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	config := f.String("config", "/etc/netgo/site.json", "config path")
	dest := f.String("output", "", "new output path")
	source := f.String("input", "", "input file")
	domain := f.String("domain", "", "control domain")
	mode := f.String("mode", "diagnostic", "deployment mode: diagnostic or 443")
	controlPort := f.Int("control-port", 0, "public HTTPS port (default 10443; 443 in compatibility mode)")
	relayPort := f.Int("relay-port", 0, "public Relay port (default 18443; 443 in compatibility mode)")
	stunPort := f.Int("stun-port", 3478, "public STUN UDP port")
	id := f.String("site-id", "site1", "site ID")
	alias := f.String("admin-alias", "admin", "local administrator alias")
	userID := f.String("admin-id", "", "existing NetBird administrator ID")
	resetUser := f.String("user-id", "", "user ID for password reset")
	passwordStdin := f.Bool("password-stdin", false, "read replacement password from stdin instead of terminal")
	email := f.String("admin-email", "", "existing administrator email")
	sources := f.String("sources", "/etc/netgo,/var/lib/netgo,/var/lib/netbird-server,/var/lib/netgo-router", "backup roots (services must be stopped)")
	digest := f.String("sha256", "", "trusted digest of release SHA256SUMS")
	socket := f.String("socket", "", "Linux diagnostic client socket")
	clientAlias := f.String("alias", "", "client username")
	replace := f.Bool("replace", false, "explicitly replace the previous user connection")
	if e := f.Parse(os.Args[start:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if cmd == "release" {
		if sub != "verify" || *source == "" {
			return errors.New("release verify --input directory --sha256 trusted manifest digest")
		}
		manifest, err := operations.Verify(*source, *digest)
		if err != nil {
			return err
		}
		return output(manifest)
	}
	if cmd == "client" {
		return clientCommand(sub, *socket, *source, *id, *clientAlias, *replace)
	}
	if cmd == "init" {
		if !site.ValidID(*id) || *domain == "" || strings.ContainsAny(*domain, "/\\\n\r: \"") {
			return errors.New("valid site ID and control domain required")
		}
		if _, e := os.Stat(*config); !os.IsNotExist(e) {
			return errors.New("config already exists")
		}
		if *controlPort == 0 {
			*controlPort = 10443
			if *mode == "443" {
				*controlPort = 443
			}
		}
		if *relayPort == 0 {
			*relayPort = 18443
			if *mode == "443" {
				*relayPort = 443
			}
		}
		origin := "https://" + *domain
		if *controlPort != 443 {
			origin += ":" + fmt.Sprint(*controlPort)
		}
		c := site.Config{Site: site.Profile{Protocol: 2, ConfigRevision: 1, ID: *id, Name: "NetGo " + *id, Origin: origin, Candidates: []site.Candidate{{ID: "local", Address: net.JoinHostPort(*domain, fmt.Sprint(*controlPort)), Kind: "direct"}}, LocalRelay: "relay." + *domain}, Listen: "127.0.0.1:8081", Database: "/var/lib/netgo/netgo.db", ManagementURL: "http://127.0.0.1:8080", ManagementTokenFile: "/etc/netgo/management.token", NativeSecretFile: "/etc/netgo/native.secret", HelperSocket: "/run/netgo-helper/helper.sock", Interface: "wt0", LANInterface: "eth0", OverlayCIDR: "100.96.0.0/16", LeaseSeconds: 120,
			Deployment: site.Deployment{Mode: *mode, LocalRelayPort: *relayPort, LocalSTUNPort: *stunPort}}
		if e := c.Validate(); e != nil {
			return e
		}
		if e := os.MkdirAll(filepath.Dir(*config), 0700); e != nil {
			return e
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		if e := securefile.Create(*config, b, 0600); e != nil {
			return e
		}
		return output(map[string]any{"created": *config, "next": "set actual LAN interface, overlay CIDR, gateway peer ID/IP; create upstream admin and protected PAT; run init-admin"})
	}
	if cmd == "restore" {
		if *source == "" || *dest == "" {
			return errors.New("--input encrypted backup and --output new directory required")
		}
		b, e := os.ReadFile(*source)
		if e != nil {
			return e
		}
		p, e := password()
		if e != nil {
			return e
		}
		plain, e := backup.Decrypt(b, p)
		clear(p)
		if e != nil {
			return e
		}
		defer clear(plain)
		return backup.Restore(plain, *dest)
	}
	if cmd == "backup" {
		if runtime.GOOS != "linux" {
			return errors.New("consistent production backup runs on Linux")
		}
		for _, svc := range []string{"netgo-server", "netbird-server", "netgo-router", "netbird"} {
			if e := exec.Command("systemctl", "is-active", "--quiet", svc).Run(); e == nil {
				return fmt.Errorf("stop %s before a consistent backup", svc)
			} else if _, ok := e.(*exec.ExitError); !ok {
				return e
			}
		}
		if *dest == "" {
			return errors.New("--output required")
		}
		if _, e := os.Stat(*dest); !os.IsNotExist(e) {
			return errors.New("backup output already exists")
		}
		plain, e := backup.Archive(strings.Split(*sources, ","))
		if e != nil {
			return e
		}
		defer clear(plain)
		p, e := password()
		if e != nil {
			return e
		}
		encrypted, e := backup.Encrypt(plain, p)
		clear(p)
		if e != nil {
			return e
		}
		return securefile.Create(*dest, encrypted, 0600)
	}
	c, e := site.Load(*config)
	if e != nil {
		return e
	}
	if cmd == "reset-password" {
		if runtime.GOOS != "linux" || os.Geteuid() != 0 {
			return errors.New("password maintenance requires root on the target Linux host")
		}
		if *resetUser == "" {
			return errors.New("reset-password --user-id required; this maintenance interrupts all connections")
		}
		var p []byte
		if *passwordStdin {
			p, e = io.ReadAll(io.LimitReader(os.Stdin, 75))
			if len(p) >= 75 {
				clear(p)
				return errors.New("password input exceeds 72 bytes plus line ending")
			}
			p = bytes.TrimRight(p, "\r\n")
		} else {
			fmt.Fprintln(os.Stderr, "密码维护将暂时停止连接；失败时保留维护记录并停止服务。")
			fmt.Fprint(os.Stderr, "新密码：")
			p, e = term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if e == nil {
				fmt.Fprint(os.Stderr, "再次输入：")
				confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if err != nil {
					e = err
				} else if string(p) != string(confirm) {
					e = errors.New("passwords do not match")
				}
				clear(confirm)
			}
		}
		defer clear(p)
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if e = (operations.PasswordTransaction{}).Reset(ctx, *config, *resetUser, p); e != nil {
			return e
		}
		return output(map[string]bool{"password_reset": true, "credentials_revoked": true, "reconnect_required": true})
	}
	if cmd == "upgrade" || cmd == "rollback" {
		if runtime.GOOS != "linux" {
			return errors.New("release operations require the target Linux host")
		}
		transaction := operations.ReleaseTransaction{ConfigPath: *config}
		if cmd == "upgrade" {
			if *source == "" || *dest == "" {
				return errors.New("--input release directory, --sha256 trusted manifest digest and --output encrypted backup required")
			}
			// Verify before reading a password or stopping production services.
			if _, e = operations.Verify(*source, *digest); e != nil {
				return e
			}
			p, err := password()
			if err != nil {
				return err
			}
			defer clear(p)
			transaction.Snapshot = func(context.Context) (string, error) {
				plain, err := backup.Archive(strings.Split(*sources, ","))
				if err != nil {
					return "", err
				}
				defer clear(plain)
				sealed, err := backup.Encrypt(plain, p)
				if err != nil {
					return "", err
				}
				if err = securefile.Create(*dest, sealed, 0600); err != nil {
					return "", err
				}
				return *dest, nil
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if cmd == "upgrade" {
			e = transaction.Upgrade(ctx, *source, *digest)
		} else {
			e = transaction.Rollback(ctx)
		}
		if e != nil {
			return e
		}
		return output(map[string]bool{"applied": true})
	}
	if cmd == "export-site" {
		if *dest == "" {
			return output(c.Site)
		}
		b, _ := json.MarshalIndent(c.Site, "", "  ")
		return os.WriteFile(*dest, b, 0600)
	}
	if cmd == "doctor" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		report := diagnostics.Doctor(ctx, c)
		if e := output(report); e != nil {
			return e
		}
		if !report.Ready {
			return errors.New("doctor found unavailable or unsafe prerequisites")
		}
		return nil
	}
	if cmd == "config" {
		switch sub {
		case "validate":
			return output(map[string]bool{"valid": true})
		case "plan":
			next := c
			if *source != "" {
				var err error
				next, err = site.Load(*source)
				if err != nil {
					return err
				}
			}
			plan, err := operations.Plan(*config, c, next)
			if err != nil {
				return err
			}
			return output(plan)
		case "apply":
			if *source != "" {
				if runtime.GOOS != "linux" {
					return errors.New("configuration activation requires target Linux")
				}
				next, err := site.Load(*source)
				if err != nil {
					return err
				}
				transaction := operations.ConfigTransaction{Reconcile: reconcileConfig}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if err = transaction.Apply(ctx, *config, next); err != nil {
					return err
				}
				return output(map[string]bool{"applied": true})
			}
			cmd = "reconcile"
		default:
			return errors.New("unknown config command")
		}
	}
	if cmd == "auxiliary" {
		if c.Site.ConfigRevision >= site.MaxConfigRevision {
			return errors.New("configuration revision exhausted")
		}
		c.Site.ConfigRevision++
		if sub == "add" {
			b, e := os.ReadFile(*source)
			if e != nil {
				return e
			}
			var a site.Auxiliary
			if e = site.Decode(strings.NewReader(string(b)), &a); e != nil {
				return e
			}
			c.Auxiliaries = []site.Auxiliary{a}
			port := fmt.Sprint(a.PublicControlPort())
			kept := []site.Candidate{}
			for _, p := range c.Site.Candidates {
				if p.Kind == "direct" {
					kept = append(kept, p)
				}
			}
			c.Site.Candidates = append(kept, site.Candidate{ID: a.ID, Address: net.JoinHostPort(a.Host, port), Kind: "auxiliary"})
			c.Site.AuxiliaryRelay = a.Relay
		} else if sub == "remove" {
			c.Auxiliaries = nil
			c.Site.AuxiliaryRelay = ""
			kept := []site.Candidate{}
			for _, p := range c.Site.Candidates {
				if p.Kind == "direct" {
					kept = append(kept, p)
				}
			}
			c.Site.Candidates = kept
		} else {
			return errors.New("unknown auxiliary command")
		}
		if e = c.Validate(); e != nil {
			return e
		}
		if *dest == "" {
			return errors.New("--output new reviewed config required; activate after relay/frp validation")
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		return securefile.Create(*dest, b, 0600)
	}
	s, e := store.Open(c.Database)
	if e != nil {
		return e
	}
	defer s.Close()
	if cmd == "diagnostics" {
		b, e := diagnostics.Report(c, s)
		if e != nil {
			return e
		}
		if *dest == "" {
			fmt.Println(string(b))
			return nil
		}
		return diagnostics.Write(*dest, b)
	}
	token, e := site.Secret(c.ManagementTokenFile)
	if e != nil {
		return e
	}
	m := &netbird.Management{URL: c.ManagementURL, Token: token, Client: &http.Client{Timeout: 10 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if cmd == "init-admin" {
		if *userID == "" || !site.ValidID(*alias) || !strings.Contains(*email, "@") {
			return errors.New("--admin-id --admin-alias --admin-email required")
		}
		if e = m.UserActive(ctx, *userID); e != nil {
			return e
		}
		return s.Update(func(st *model.State) error {
			if len(st.Users) != 0 {
				return errors.New("administrator bootstrap already completed")
			}
			st.Users[*userID] = model.User{ID: *userID, Alias: *alias, Email: *email, Role: "admin", MaxDevices: 20, MaxSessions: 1, Resources: []string{}}
			st.Record("local", "bootstrap", *userID)
			return nil
		})
	}
	if cmd == "reconcile" {
		r := netbird.Reconciler{Management: m, Store: s, SiteID: c.Site.ID, GatewayID: c.Site.GatewayPeerID, LANInterface: c.LANInterface, Bypass: c.Bypass}
		return r.Apply(ctx)
	}
	return errors.New("unknown command")
}

func reconcileConfig(ctx context.Context, c site.Config) error {
	s, err := store.Open(c.Database)
	if err != nil {
		return err
	}
	defer s.Close()
	token, err := site.Secret(c.ManagementTokenFile)
	if err != nil {
		return err
	}
	m := &netbird.Management{URL: c.ManagementURL, Token: token, Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("management redirect forbidden") }}}
	r := netbird.Reconciler{Management: m, Store: s, SiteID: c.Site.ID, GatewayID: c.Site.GatewayPeerID, LANInterface: c.LANInterface, Bypass: c.Bypass}
	return r.Apply(ctx)
}
