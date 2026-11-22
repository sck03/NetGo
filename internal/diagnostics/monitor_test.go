package diagnostics

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"netgo/internal/model"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRuntimeAlertsAreBoundedRedactedAndNeverInventNetworkAcceptance(t *testing.T) {
	m := NewMonitor()
	c := site.Config{Site: site.Profile{ID: "site", ConfigRevision: 4}, NativeSecretFile: "secret-path"}
	st := model.NewState()
	st.LastError = "sensitive-token"
	st.Users["private-account"] = model.User{ID: "private-account", Email: "private@example.test"}
	st.Credentials["secret-token"] = model.Credential{UserID: "private-account"}
	if m.Snapshot(c, st).Status != "critical" {
		t.Fatal("missing sample reported healthy")
	}
	m.Probe = func(context.Context, site.Config) ([]Check, HostResources) {
		return []Check{{Name: "frpc", Status: "FAIL", Detail: "反向通道不可用"}}, HostResources{DiskTotalBytes: 1024}
	}
	m.Sample(context.Background(), c)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); m.Observe(401, true); _ = m.Snapshot(c, st) }()
	}
	wg.Wait()
	r := m.Snapshot(c, st)
	b, _ := json.Marshal(r)
	for _, secret := range []string{"private-account", "private@example.test", "secret-token", "sensitive-token", "secret-path"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("diagnostic leaked %s", secret)
		}
	}
	if r.Status != "critical" || r.AuthenticationFailures != 20 || r.Acceptance != "NOT-RUN" || len(r.Alerts) != 3 {
		t.Fatalf("incorrect runtime status: %s", b)
	}
	m.mu.Lock()
	m.sampled = time.Now().Add(-2 * time.Minute)
	m.mu.Unlock()
	if m.Snapshot(c, model.NewState()).Status != "critical" {
		t.Fatal("stale sample reported healthy")
	}
}

func TestConfiguredCertificateRenewalAndHostnameAlerts(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		name       string
		days       int
		host, want string
	}{{"valid", 30, "site.test", "PASS"}, {"renew", 7, "site.test", "WARN"}, {"expired", -1, "site.test", "FAIL"}, {"wrong-host", 30, "other.test", "FAIL"}} {
		t.Run(tc.name, func(t *testing.T) {
			cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(time.Duration(tc.days) * 24 * time.Hour), DNSNames: []string{tc.host}}
			der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "control.crt")
			if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			if result := CertificateCheck(path, "https://site.test:10443", now); result.Status != tc.want {
				t.Fatal(result)
			}
		})
	}
}

func TestDebianBaselineDoesNotTrustIDLikeOrDuplicateFields(t *testing.T) {
	if !Debian13Release("ID=debian\nVERSION_ID=\"13\"\n") {
		t.Fatal("Debian 13 rejected")
	}
	for _, release := range []string{"ID=ubuntu\nID_LIKE=debian\nVERSION_ID=13\n", "ID=debian\nVERSION_ID=12\n", "ID=debian\nVERSION_ID=13\nID=ubuntu\n"} {
		if Debian13Release(release) {
			t.Fatal("unsupported release accepted")
		}
	}
}
