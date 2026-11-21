package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/term"
	"net"
	"net/http"
	"netgo/internal/backup"
	"netgo/internal/diagnostics"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
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
	if len(os.Args) < 2 {
		return errors.New("commands: init, doctor, config validate|plan|apply, export-site, reconcile, auxiliary add|remove, diagnostics, backup, restore, version")
	}
	cmd := os.Args[1]
	if cmd == "version" {
		return output(map[string]string{"version": model.Version, "go": runtime.Version()})
	}
	sub := ""
	start := 2
	if cmd == "config" || cmd == "auxiliary" {
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
	id := f.String("site-id", "site1", "site ID")
	alias := f.String("admin-alias", "admin", "local administrator alias")
	userID := f.String("admin-id", "", "existing NetBird administrator ID")
	email := f.String("admin-email", "", "existing administrator email")
	sources := f.String("sources", "/etc/netgo,/var/lib/netgo,/var/lib/netbird-server", "backup roots (services must be stopped)")
	if e := f.Parse(os.Args[start:]); e != nil {
		return e
	}
	if cmd == "init" {
		if !site.ValidID(*id) || *domain == "" || strings.ContainsAny(*domain, "/\\\n\r: \"") {
			return errors.New("valid site ID and control domain required")
		}
		if _, e := os.Stat(*config); !os.IsNotExist(e) {
			return errors.New("config already exists")
		}
		c := site.Config{Site: site.Profile{Protocol: 1, ID: *id, Name: "NetGo " + *id, Origin: "https://" + *domain, Candidates: []site.Candidate{{ID: "local", Address: *domain + ":443", Kind: "direct"}}, LocalRelay: "relay." + *domain}, Listen: "127.0.0.1:8081", Database: "/var/lib/netgo/netgo.db", ManagementURL: "http://127.0.0.1:8080", ManagementTokenFile: "/etc/netgo/management.token", NativeSecretFile: "/etc/netgo/native.secret", HelperSocket: "/run/netgo-helper/helper.sock", Interface: "wt0", LANInterface: "eth0", OverlayCIDR: "100.96.0.0/16", LeaseSeconds: 120}
		if e := c.Validate(); e != nil {
			return e
		}
		if e := os.MkdirAll(filepath.Dir(*config), 0700); e != nil {
			return e
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		if e := os.WriteFile(*config, b, 0600); e != nil {
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
		return backup.Restore(plain, *dest)
	}
	if cmd == "backup" {
		if runtime.GOOS != "linux" {
			return errors.New("consistent production backup runs on Linux")
		}
		for _, svc := range []string{"netgo-server", "netbird-server", "netbird"} {
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
		p, e := password()
		if e != nil {
			return e
		}
		encrypted, e := backup.Encrypt(plain, p)
		clear(p)
		if e != nil {
			return e
		}
		return os.WriteFile(*dest, encrypted, 0600)
	}
	c, e := site.Load(*config)
	if e != nil {
		return e
	}
	if cmd == "export-site" {
		if *dest == "" {
			return output(c.Site)
		}
		b, _ := json.MarshalIndent(c.Site, "", "  ")
		return os.WriteFile(*dest, b, 0600)
	}
	if cmd == "doctor" {
		issues := []string{}
		for _, p := range []string{c.ManagementTokenFile, c.NativeSecretFile} {
			if _, e := site.Secret(p); e != nil {
				issues = append(issues, "secret file unavailable: "+p)
			}
		}
		for _, name := range []string{"nft", "netbird", "netbird-server", "caddy"} {
			if _, e := exec.LookPath(name); e != nil {
				issues = append(issues, "missing executable: "+name)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(c.ManagementURL, "http://"))
		if e != nil {
			issues = append(issues, "local management unavailable")
		} else {
			conn.Close()
		}
		return output(map[string]any{"os": runtime.GOOS, "issues": issues, "real_network_acceptance": "NOT-RUN"})
	}
	if cmd == "config" {
		switch sub {
		case "validate":
			return output(map[string]bool{"valid": true})
		case "plan":
			return output(map[string]any{"site": c.Site.ID, "owned_table": "inet netgo", "interface": c.Interface, "local_relay_required": true, "auxiliaries": len(c.Auxiliaries), "apply": "run reconcile after reviewing upstream policies"})
		case "apply":
			cmd = "reconcile"
		default:
			return errors.New("unknown config command")
		}
	}
	if cmd == "auxiliary" {
		if sub == "add" {
			b, e := os.ReadFile(*source)
			if e != nil {
				return e
			}
			var a site.Auxiliary
			if e = json.Unmarshal(b, &a); e != nil {
				return e
			}
			c.Auxiliaries = []site.Auxiliary{a}
			port := "8443"
			if a.Mode == "443" {
				port = "443"
			}
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
		return os.WriteFile(*dest, b, 0600)
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
			st.Users[*userID] = model.User{ID: *userID, Alias: *alias, Email: *email, Role: "admin", MaxDevices: 20, Resources: []string{}}
			st.Record("local", "bootstrap", *userID)
			return nil
		})
	}
	if cmd == "reconcile" {
		r := netbird.Reconciler{Management: m, Store: s, SiteID: c.Site.ID, GatewayID: c.Site.GatewayPeerID}
		return r.Apply(ctx)
	}
	return errors.New("unknown command")
}
