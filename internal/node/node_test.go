package node

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"netgo/internal/deployment"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func configFixture(t *testing.T) site.Config {
	t.Helper()
	var c site.Config
	if err := readJSON("../../examples/site.json", &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func physicalTemp(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func bundleFixture(t *testing.T) BundleData {
	t.Helper()
	c := configFixture(t)
	a := site.Auxiliary{ID: "vps1", Host: "control.vps.example", Relay: "rels://relay.vps.example:28443", STUN: "stun:relay.vps.example:3478", TunnelHost: "tunnel.vps.example", TokenFile: "/etc/netgo/native.secret", Mode: "diagnostic", ControlPort: 24443, TunnelPort: 27000}
	c.Auxiliaries = []site.Auxiliary{a}
	c.Site.AuxiliaryRelay = a.Relay
	c.Site.Candidates = append(c.Site.Candidates, site.Candidate{ID: a.ID, Kind: "auxiliary", Address: a.Host + ":24443"})
	secrets, err := deployment.NewSecrets()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := deployment.Render(c, secrets)
	if err != nil {
		t.Fatal(err)
	}
	m := Manager{Role: "server", Root: physicalTemp(t)}
	u, _ := url.Parse(a.Relay)
	certs, err := m.certificates(map[string]string{"relay": u.Hostname(), "tunnel": a.TunnelHost}, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	data := BundleData{Info: Bundle{Schema: 2, Owner: "NetGo VPS bundle", SiteID: c.Site.ID, ControlOrigin: c.Site.Origin, Auxiliary: a, Node: *rendered.Auxiliary, Secrets: NodeSecrets{secrets.Relay, secrets.FRP}, Files: map[string]string{}}, Files: map[string][]byte{}}
	for name, b := range rendered.Files {
		if strings.HasPrefix(name, "auxiliary/") {
			data.Files[name] = b
		}
	}
	for _, name := range []string{"relay.crt", "relay.key", "tunnel.crt", "tunnel.key", "ca.crt"} {
		data.Files["tls/"+name] = certs[name]
	}
	for name, b := range data.Files {
		data.Info.Files[name] = digest(b)
	}
	if err = data.Validate(); err != nil {
		t.Fatal(err)
	}
	return data
}
func TestVPSBundleHasOnlyNodeSecretsAndRejectsTampering(t *testing.T) {
	data := bundleFixture(t)
	path := filepath.Join(t.TempDir(), "node.zip")
	if err := writeBundle(path, data); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 7 {
		t.Fatalf("unexpected payload: %v", got.Files)
	}
	for name := range got.Files {
		if name == "tls/ca.key" || strings.Contains(name, "control") || strings.Contains(name, "native") {
			t.Fatal("site secret leaked", name)
		}
	}
	got.Files["auxiliary/frps.toml"] = append(got.Files["auxiliary/frps.toml"], []byte("webServer.port = 8080\n")...)
	got.Info.Files["auxiliary/frps.toml"] = digest(got.Files["auxiliary/frps.toml"])
	if err = got.Validate(); err == nil {
		t.Fatal("arbitrary frps settings accepted even with a rewritten digest")
	}
}
func TestVPSPortChangeKeepsKeysAndValidatesAllListeners(t *testing.T) {
	data := bundleFixture(t)
	key := digest(data.Files["tls/relay.key"])
	data.Info.Auxiliary.ControlPort = 25443
	data.Info.Auxiliary.TunnelPort = 28000
	data.Info.Auxiliary.Relay = "rels://relay.vps.example:29443"
	data.Info.Auxiliary.STUN = "stun:relay.vps.example:13478"
	if err := data.rebuild(); err != nil {
		t.Fatal(err)
	}
	if key != digest(data.Files["tls/relay.key"]) {
		t.Fatal("port change rotated identity")
	}
	data.Info.Auxiliary.TunnelPort = 25443
	if err := data.rebuild(); err == nil {
		t.Fatal("conflicting listener accepted")
	}
}
func TestCertificateImportRejectsWrongHostAndKey(t *testing.T) {
	data := bundleFixture(t)
	files := map[string][]byte{"relay.crt": data.Files["tls/relay.crt"], "relay.key": data.Files["tls/relay.key"], "ca.crt": data.Files["tls/ca.crt"]}
	if err := validateCertificates(map[string]string{"relay": "wrong.example"}, files); err == nil {
		t.Fatal("wrong host accepted")
	}
	files["relay.key"] = data.Files["tls/tunnel.key"]
	if err := validateCertificates(map[string]string{"relay": "relay.vps.example"}, files); err == nil {
		t.Fatal("mismatched private key accepted")
	}
}
func fakePackage(t *testing.T) string {
	t.Helper()
	root := physicalTemp(t)
	elf := make([]byte, 64)
	copy(elf, []byte("\x7fELF\x02"))
	elf[18] = 62
	for _, name := range []string{"frps", "netbird-relay", "netgo-vpsctl"} {
		path := filepath.Join(root, "dist/vps/bin", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, elf, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestVPSInstallModifyUninstallAndReinstallPreserveUnrelatedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX systemd and symlink lifecycle")
	}
	active := map[string]bool{}
	fail := false
	m := Manager{Role: "vps", Root: physicalTemp(t), Out: &bytes.Buffer{}, Run: func(_ context.Context, name string, args []string, _ []byte, _ []string) ([]byte, error) {
		if name == "systemctl" && len(args) > 1 {
			unit := args[len(args)-1]
			switch args[0] {
			case "is-active":
				if !active[unit] {
					return nil, errors.New("inactive")
				}
			case "start":
				if fail && unit == "netgo-vps-relay" {
					return nil, errors.New("injected failure")
				}
				active[unit] = true
			case "stop":
				active[unit] = false
			}
		}
		return nil, nil
	}}
	source := fakePackage(t)
	if err := m.Install(source); err != nil {
		t.Fatal(err)
	}
	data := bundleFixture(t)
	if err := m.ConfigureVPS(data); err != nil {
		t.Fatal(err)
	}
	unrelated := m.path("/etc/systemd/system/unrelated.service")
	if err := os.WriteFile(unrelated, []byte("unrelated"), 0644); err != nil {
		t.Fatal(err)
	}
	oldConfig, _ := os.ReadFile(filepath.Join(m.etc(), "relay.env"))
	data.Info.Auxiliary.ControlPort = 25443
	if err := data.rebuild(); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := m.ConfigureVPS(data); err == nil {
		t.Fatal("injected service failure passed")
	}
	restored, _ := os.ReadFile(filepath.Join(m.etc(), "relay.env"))
	if !bytes.Equal(restored, oldConfig) {
		t.Fatal("configuration not restored")
	}
	fail = false
	if err := m.ConfigureVPS(data); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.etc(), "tls/relay.key")); err != nil {
		t.Fatal("uninstall lost identity")
	}
	if b, err := os.ReadFile(unrelated); err != nil || string(b) != "unrelated" {
		t.Fatal("unrelated unit changed")
	}
	if err := m.Install(source); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureVPS(data); err != nil {
		t.Fatal(err)
	}
}
func TestForeignUnitIsNeverRemovedByFailedVPSConfiguration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink lifecycle")
	}
	m := Manager{Role: "vps", Root: physicalTemp(t), Out: &bytes.Buffer{}, Run: func(context.Context, string, []string, []byte, []string) ([]byte, error) { return nil, nil }}
	if err := m.Install(fakePackage(t)); err != nil {
		t.Fatal(err)
	}
	path := m.path("/etc/systemd/system/netgo-vps-relay.service")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("foreign"), 0644)
	if err := m.ConfigureVPS(bundleFixture(t)); err == nil {
		t.Fatal("foreign unit was replaced")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "foreign" {
		t.Fatal("foreign unit lost in rollback")
	}
}
func TestPackageArchiveRejectsTraversalBeforeWriting(t *testing.T) {
	root := physicalTemp(t)
	path := filepath.Join(root, "bad.zip")
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	entry, _ := z.Create("../escape")
	entry.Write([]byte("bad"))
	z.Close()
	f.Close()
	hash, _ := sha(path)
	if _, _, err := stagePackage(path, hash); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatal("archive wrote outside its root")
	}
}
func TestIdentityErrorsNeverExposeResponseSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"token": "do-not-print"})
	}))
	defer server.Close()
	m := Manager{Role: "server", Root: physicalTemp(t)}
	err := m.API(site.Config{ManagementURL: server.URL}, "/setup", "POST", nil, nil)
	if err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("private identity response exposed", err)
	}
}

func TestCertificateGenerationNeverReplacesIncompleteCA(t *testing.T) {
	m := Manager{Role: "server", Root: physicalTemp(t)}
	source := filepath.Join(m.etc(), "tls")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("existing incomplete CA")
	if err := os.WriteFile(filepath.Join(source, "ca.crt"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.certificates(map[string]string{"control": "site.example"}, "", true); err == nil {
		t.Fatal("incomplete CA was silently replaced")
	}
	saved, _ := os.ReadFile(filepath.Join(source, "ca.crt"))
	if !bytes.Equal(saved, original) {
		t.Fatal("existing CA modified")
	}
	if _, err := m.certificates(map[string]string{"control": "site.example"}, "", false); err == nil {
		t.Fatal("empty import directory accepted")
	}
}

func TestFailedInstallCanRetryWithoutRemovingForeignShortcut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink lifecycle")
	}
	m := Manager{Role: "vps", Root: physicalTemp(t), Out: &bytes.Buffer{}, Run: func(context.Context, string, []string, []byte, []string) ([]byte, error) { return nil, nil }}
	path := m.path("/usr/local/bin/ngv")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign shortcut"), 0600); err != nil {
		t.Fatal(err)
	}
	source := fakePackage(t)
	if err := m.Install(source); err == nil {
		t.Fatal("foreign shortcut overwritten")
	}
	saved, _ := os.ReadFile(path)
	if string(saved) != "foreign shortcut" {
		t.Fatal("foreign shortcut was removed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(source); err != nil {
		t.Fatal("clean retry blocked by failed installation", err)
	}
	state, err := m.installation()
	if err != nil || state.Status != "installed" {
		t.Fatal(state.Status, err)
	}
}
