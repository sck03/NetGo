package node

import (
	"encoding/json"
	"errors"
	"io/fs"
	"netgo/internal/deployment"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m Manager) ApplyServerConfig(next site.Config, certificates map[string][]byte) (result error) {
	old, err := m.config()
	if err != nil {
		return err
	}
	installed, err := m.installation()
	if err != nil {
		return err
	}
	if err = m.verifyInstalled(installed); err != nil {
		return err
	}
	if next.Site.ID != old.Site.ID || next.Database != old.Database || next.ManagementURL != old.ManagementURL || next.OverlayCIDR != old.OverlayCIDR || next.Interface != old.Interface || next.LANInterface != old.LANInterface {
		return errors.New("站点身份和网络接口须通过首次配置设置")
	}
	if next.Site.ConfigRevision != old.Site.ConfigRevision || old.Site.ConfigRevision >= site.MaxConfigRevision-1 {
		return errors.New("配置版本已变化，请重新打开设置")
	}
	next.Site.ConfigRevision = old.Site.ConfigRevision + 1
	if err = next.Validate(); err != nil {
		return err
	}
	var secrets deployment.Secrets
	if err = readJSON(filepath.Join(m.etc(), "rendered/secrets.json"), &secrets); err != nil {
		return err
	}
	rendered, err := deployment.Render(next, secrets)
	if err != nil {
		return err
	}
	if next.Deployment.Mode == "443" {
		if err = m.haproxy(); err != nil {
			return err
		}
	}
	files := map[string][]byte{}
	for name, b := range rendered.Files {
		files["rendered/"+name] = b
		if strings.HasPrefix(name, "local/") {
			files[strings.TrimPrefix(name, "local/")] = b
		}
	}
	files["site.json"], _ = json.MarshalIndent(next, "", "  ")
	files["client-site.json"], _ = json.MarshalIndent(next.Site, "", "  ")
	roots, _ := os.ReadFile(m.path("/etc/ssl/certs/ca-certificates.crt"))
	files["tls/trust.pem"] = append(append(roots, '\n'), []byte(next.Site.CA)...)
	for name, b := range certificates {
		if name == "trust.pem" {
			continue
		}
		files["tls/"+name] = b
	}
	removed := []string{}
	for _, name := range []string{"frpc.toml", "haproxy.cfg", "relay-port.nft"} {
		if _, ok := files[name]; !ok {
			removed = append(removed, name)
		}
	}
	if certificates != nil && next.Site.CA != old.Site.CA {
		for _, name := range []string{"ca.crt", "ca.key"} {
			if _, ok := certificates[name]; !ok {
				removed = append(removed, "tls/"+name)
			}
		}
	}
	saved := map[string][]byte{}
	missing := map[string]bool{}
	for name := range files {
		b, e := os.ReadFile(filepath.Join(m.etc(), name))
		if os.IsNotExist(e) {
			missing[name] = true
		} else if e != nil {
			return e
		} else {
			saved[name] = b
		}
	}
	for _, name := range removed {
		b, e := os.ReadFile(filepath.Join(m.etc(), name))
		if e == nil {
			saved[name] = b
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	journal := filepath.Join(m.etc(), "maintenance", time.Now().UTC().Format("20060102-150405.000000000"))
	for name, b := range saved {
		if err = m.write(filepath.Join(journal, "previous", name), b, 0600); err != nil {
			return err
		}
	}
	active := []string{}
	enabled := []string{}
	for _, unit := range installed.Units {
		if m.active(unit) {
			active = append(active, unit)
		}
		if _, e := m.command("systemctl", "is-enabled", "--quiet", unit); e == nil {
			enabled = append(enabled, unit)
		}
	}
	touched := false
	defer func() {
		if result == nil || !touched {
			return
		}
		for i := len(installed.Units) - 1; i >= 0; i-- {
			_ = m.service("stop", installed.Units[i])
		}
		recovered := true
		for name := range missing {
			if e := os.Remove(filepath.Join(m.etc(), name)); e != nil && !os.IsNotExist(e) {
				recovered = false
			}
		}
		for name, b := range saved {
			if e := m.write(filepath.Join(m.etc(), name), b, configMode(name)); e != nil {
				recovered = false
			}
		}
		old.Site.ConfigRevision = next.Site.ConfigRevision + 1
		if e := m.writeConfig(old); e != nil {
			recovered = false
		}
		if e := m.json(filepath.Join(m.etc(), "client-site.json"), old.Site); e != nil {
			recovered = false
		}
		for _, unit := range installed.Units {
			_ = m.service("disable", unit)
		}
		if e := m.service("enable", enabled...); e != nil {
			recovered = false
		}
		if e := m.service("start", active...); e != nil {
			recovered = false
		}
		var state SetupState
		if readJSON(filepath.Join(m.etc(), "setup.json"), &state) == nil && state.Complete {
			if e := m.connectRouter(old, state); e != nil {
				recovered = false
			}
		}
		m.print("修改失败，原配置恢复完成：%t。私有诊断目录：%s", recovered, journal)
	}()
	touched = true
	for i := len(active) - 1; i >= 0; i-- {
		if err = m.service("stop", active[i]); err != nil {
			return err
		}
	}
	for name, b := range files {
		if err = m.write(filepath.Join(m.etc(), name), b, configMode(name)); err != nil {
			return err
		}
	}
	for _, name := range removed {
		if err = os.Remove(filepath.Join(m.etc(), name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if _, err = m.command(filepath.Join(m.base(), "upstream/caddy"), "validate", "--config", filepath.Join(m.etc(), "Caddyfile"), "--adapter", "caddyfile"); err != nil {
		return err
	}
	if len(next.Auxiliaries) > 0 {
		if _, err = m.command(filepath.Join(m.base(), "upstream/frpc"), "verify", "-c", filepath.Join(m.etc(), "frpc.toml")); err != nil {
			return err
		}
	}
	if err = m.StartServer(); err != nil {
		return err
	}
	for _, unit := range installed.Units {
		if err = m.service("disable", unit); err != nil {
			return err
		}
	}
	if err = m.service("enable", wantedUnits(next)...); err != nil {
		return err
	}
	m.print("设置已生效。客户端导入文件已更新：%s", filepath.Join(m.etc(), "client-site.json"))
	return nil
}
func configMode(name string) fs.FileMode {
	if strings.HasPrefix(name, "rendered/") || name == "tls/ca.key" || name == "client-site.json" {
		return 0600
	}
	return 0640
}
func (m Manager) haproxy() error {
	if _, err := execLookPath("haproxy"); err == nil {
		return nil
	}
	if m.active("haproxy") {
		return errors.New("已有 HAProxy 服务，请保留现有端口")
	}
	if _, err := m.command("systemctl", "mask", "--runtime", "haproxy"); err != nil {
		return err
	}
	defer m.command("systemctl", "unmask", "--runtime", "haproxy")
	if _, err := m.command("apt-get", "update"); err != nil {
		return err
	}
	if _, err := m.command("apt-get", "install", "-y", "--no-upgrade", "--no-install-recommends", "haproxy"); err != nil {
		return err
	}
	return m.service("disable", "haproxy")
}
