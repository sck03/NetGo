package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (data *BundleData) rebuild() error {
	old := data.Info.Node
	a := data.Info.Auxiliary
	if err := a.Validate(); err != nil {
		return err
	}
	u, _ := url.Parse(a.Relay)
	_, stun, _ := net.SplitHostPort(strings.TrimPrefix(a.STUN, "stun:"))
	port := deploymentPort(u.Port(), 0)
	sp := deploymentPort(stun, 0)
	data.Info.Node.Mode = a.Mode
	data.Info.Node.ControlPort = a.PublicControlPort()
	data.Info.Node.ControlBackendPort = a.PublicControlPort()
	data.Info.Node.TunnelPort = a.PublicTunnelPort()
	data.Info.Node.TunnelBindPort = a.PublicTunnelPort()
	data.Info.Node.RelayPort = port
	data.Info.Node.RelayBindPort = port
	data.Info.Node.RelayEndpoint = a.Relay
	data.Info.Node.STUNPort = sp
	if a.Mode != "diagnostic" {
		return errors.New("端口修改使用独立端口模式")
	}
	text := string(data.Files["auxiliary/frps.toml"])
	text = strings.ReplaceAll(text, `bindAddr = "127.0.0.1"`, `bindAddr = "0.0.0.0"`)
	text = strings.ReplaceAll(text, `proxyBindAddr = "127.0.0.1"`, `proxyBindAddr = "0.0.0.0"`)
	text = strings.ReplaceAll(text, fmt.Sprintf("bindPort = %d", old.TunnelBindPort), fmt.Sprintf("bindPort = %d", a.PublicTunnelPort()))
	text = strings.ReplaceAll(text, fmt.Sprintf("single = %d", old.ControlBackendPort), fmt.Sprintf("single = %d", a.PublicControlPort()))
	data.Files["auxiliary/frps.toml"] = []byte(text)
	lines := strings.Split(string(data.Files["auxiliary/relay.env"]), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "NB_LISTEN_ADDRESS":
			lines[i] = fmt.Sprintf("%s=:%d", key, port)
		case "NB_EXPOSED_ADDRESS":
			lines[i] = key + "=" + a.Relay
		case "NB_STUN_PORTS":
			lines[i] = fmt.Sprintf("%s=%d", key, sp)
		}
	}
	data.Files["auxiliary/relay.env"] = []byte(strings.Join(lines, "\n"))
	delete(data.Files, "auxiliary/haproxy.cfg")
	delete(data.Files, "auxiliary/relay-port.nft")
	data.Info.Files = map[string]string{}
	for name, b := range data.Files {
		data.Info.Files[name] = digest(b)
	}
	return data.Validate()
}
func (m Manager) vpsUnit(name string, privateCA bool) []byte {
	prefix := "# Managed by " + Owner + "\n[Unit]\nDescription=NetGo VPS " + name + "\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\n"
	if name == "redirect" {
		return []byte(prefix + "Type=oneshot\nRemainAfterExit=yes\nExecStart=/usr/sbin/nft -f " + filepath.Join(m.etc(), "relay-port.nft") + "\nExecStop=" + filepath.Join(m.base(), "bin/netgo-vpsctl") + " redirect-remove\n\n[Install]\nWantedBy=multi-user.target\n")
	}
	commands := map[string]string{"frps": "ExecStart=" + filepath.Join(m.base(), "bin/frps") + " -c " + filepath.Join(m.etc(), "frps.toml") + "\n", "relay": "EnvironmentFile=" + filepath.Join(m.etc(), "relay.env") + "\nExecStart=" + filepath.Join(m.base(), "bin/netbird-relay") + "\n", "haproxy": "ExecStart=/usr/sbin/haproxy -W -db -f " + filepath.Join(m.etc(), "haproxy.cfg") + "\n"}
	if name == "relay" && privateCA {
		commands[name] = "Environment=SSL_CERT_FILE=" + filepath.Join(m.etc(), "tls/ca.crt") + "\n" + commands[name]
	}
	return []byte(prefix + "User=netgo-vps\nGroup=netgo-vps\n" + commands[name] + "Restart=on-failure\nRestartSec=3\nNoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nAmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\n\n[Install]\nWantedBy=multi-user.target\n")
}
func (m Manager) ConfigureVPS(data BundleData) (result error) {
	if err := data.Validate(); err != nil {
		return err
	}
	state, err := m.installation()
	if err != nil {
		return err
	}
	if state.Status == "uninstalled" {
		return errors.New("程序已卸载，请先重新执行独立 VPS 安装脚本")
	}
	if err = m.verifyInstalled(state); err != nil {
		return err
	}
	if old := new(Bundle); readJSON(filepath.Join(m.etc(), "vps.json"), old) == nil && old.SiteID != data.Info.SiteID {
		return errors.New("该节点属于其他站点，请保留配置后使用新的节点")
	}
	if data.Info.Node.Mode == "443" {
		if err = m.haproxy(); err != nil {
			return err
		}
	}
	previous := state
	previous.Files = maps.Clone(state.Files)
	previous.Links = maps.Clone(state.Links)
	previous.Units = append([]string{}, state.Units...)
	journal := filepath.Join(m.base(), "configuration-backups", time.Now().UTC().Format("20060102-150405.000000000"))
	if err = os.MkdirAll(journal, 0700); err != nil {
		return err
	}
	saved := map[string][]byte{}
	configNames := []string{"frps.toml", "relay.env", "haproxy.cfg", "relay-port.nft", "vps.json", "bundle.zip", "tls/relay.crt", "tls/relay.key", "tls/tunnel.crt", "tls/tunnel.key", "tls/ca.crt"}
	for _, name := range configNames {
		path := filepath.Join(m.etc(), name)
		if b, e := os.ReadFile(path); e == nil {
			saved[path] = b
			if e = m.write(filepath.Join(journal, "config", name), b, 0600); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	for _, unit := range state.Units {
		path := m.path("/etc/systemd/system/" + unit + ".service")
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		saved[path] = b
	}
	active := []string{}
	for _, unit := range state.Units {
		if m.active(unit) {
			active = append(active, unit)
		}
	}
	desired := []string{"netgo-vps-frps", "netgo-vps-relay"}
	if data.Info.Node.Mode == "443" {
		desired = append(desired, "netgo-vps-redirect", "netgo-vps-haproxy")
	}
	for _, unit := range desired {
		path := m.path("/etc/systemd/system/" + unit + ".service")
		if _, err := os.Lstat(path); err == nil {
			if _, owned := state.Files[path]; !owned {
				return errors.New("已有非受管 VPS systemd 单元，保留原文件")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	touched := false
	defer func() {
		if result == nil || !touched {
			return
		}
		_ = m.service("stop", desired...)
		recovered := true
		paths := []string{}
		for _, name := range configNames {
			paths = append(paths, filepath.Join(m.etc(), name))
		}
		for _, unit := range desired {
			paths = append(paths, m.path("/etc/systemd/system/"+unit+".service"))
		}
		for path := range saved {
			paths = append(paths, path)
		}
		for _, path := range paths {
			if b, ok := saved[path]; ok {
				mode := os.FileMode(0640)
				if strings.HasSuffix(path, ".service") {
					mode = 0644
				}
				if e := m.write(path, b, mode); e != nil {
					recovered = false
				}
			} else if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
				recovered = false
			}
		}
		if e := m.saveInstallation(previous); e != nil {
			recovered = false
		}
		_, _ = m.command("systemctl", "daemon-reload")
		if e := m.service("start", active...); e != nil {
			recovered = false
		}
		m.print("新设置失败，原配置恢复完成：%t。私有诊断：%s", recovered, journal)
	}()
	touched = true
	for i := len(state.Units) - 1; i >= 0; i-- {
		if err = m.service("stop", state.Units[i]); err != nil {
			return err
		}
	}
	if m.Root == "" {
		ports := []int{data.Info.Node.ControlBackendPort, data.Info.Node.TunnelBindPort, data.Info.Node.RelayBindPort, 9001, 9091}
		if data.Info.Node.Mode == "443" {
			ports = append(ports, 443)
		}
		if err = checkPorts(ports, []int{data.Info.Node.RelayPort, data.Info.Node.STUNPort}); err != nil {
			return err
		}
	}
	for _, name := range configNames {
		if err = os.Remove(filepath.Join(m.etc(), name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for name, b := range data.Files {
		target := strings.TrimPrefix(name, "auxiliary/")
		if strings.HasPrefix(name, "auxiliary/") {
			text := strings.ReplaceAll(string(b), "/etc/netgo/tls/", filepath.Join(m.etc(), "tls")+"/")
			if strings.HasSuffix(name, "haproxy.cfg") {
				text = strings.ReplaceAll(strings.ReplaceAll(text, " user haproxy\n", ""), " group haproxy\n", "")
			}
			if strings.HasSuffix(name, ".nft") {
				text = strings.ReplaceAll(strings.ReplaceAll(text, "netgo_relay", "netgo_vps_relay"), "NetGo relay redirect v1", Owner+" relay redirect")
			}
			b = []byte(text)
		}
		if err = m.write(filepath.Join(m.etc(), target), b, 0640); err != nil {
			return err
		}
	}
	if err = m.json(filepath.Join(m.etc(), "vps.json"), data.Info); err != nil {
		return err
	}
	if err = writeBundle(filepath.Join(m.etc(), "bundle.zip"), data); err != nil {
		return err
	}
	if _, err = m.command(filepath.Join(m.base(), "bin/frps"), "verify", "-c", filepath.Join(m.etc(), "frps.toml")); err != nil {
		return err
	}
	if data.Info.Node.Mode == "443" {
		if _, err = m.command("haproxy", "-c", "-f", filepath.Join(m.etc(), "haproxy.cfg")); err != nil {
			return err
		}
		if _, err = m.command("nft", "-c", "-f", filepath.Join(m.etc(), "relay-port.nft")); err != nil {
			return err
		}
	}
	for _, unit := range desired {
		path := m.path("/etc/systemd/system/" + unit + ".service")
		if _, e := os.Stat(path); e == nil {
			if _, ok := state.Files[path]; !ok {
				return errors.New("已有非受管 VPS systemd 单元")
			}
		}
		b := m.vpsUnit(strings.TrimPrefix(unit, "netgo-vps-"), len(data.Files["tls/ca.crt"]) > 0)
		if err = m.write(path, b, 0644); err != nil {
			return err
		}
		state.Files[path] = digest(b)
	}
	state.Units = desired
	if err = m.saveInstallation(state); err != nil {
		return err
	}
	if _, err = m.command("systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, unit := range desired {
		if err = m.service("start", unit); err != nil {
			return err
		}
	}
	if err = wait("VPS 服务", m.DoctorVPS); err != nil {
		return err
	}
	if err = m.service("enable", desired...); err != nil {
		return err
	}
	for _, unit := range previous.Units {
		found := false
		for _, next := range desired {
			found = found || next == unit
		}
		if !found {
			_ = m.service("disable", unit)
			path := m.path("/etc/systemd/system/" + unit + ".service")
			if err = os.Remove(path); err != nil {
				return err
			}
			delete(state.Files, path)
		}
	}
	state.Status = "active"
	if err = m.saveInstallation(state); err != nil {
		return err
	}
	_, err = m.command("systemctl", "daemon-reload")
	if err != nil {
		return err
	}
	m.print("VPS 受管服务已启动，配置和本机健康检查通过。")
	return nil
}
func (m Manager) DoctorVPS() error {
	state, err := m.installation()
	if err != nil {
		return err
	}
	if err = m.verifyInstalled(state); err != nil {
		return err
	}
	if len(state.Units) == 0 {
		return errors.New("程序已安装，请导入 LAN 生成的节点配置包")
	}
	for _, unit := range state.Units {
		if !m.active(unit) {
			return errors.New("服务未启动：" + unit)
		}
	}
	if m.Root != "" {
		return nil
	}
	var info Bundle
	if err = readJSON(filepath.Join(m.etc(), "vps.json"), &info); err != nil {
		return err
	}
	for _, port := range []int{info.Node.TunnelBindPort, info.Node.RelayBindPort} {
		conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if e != nil {
			return e
		}
		conn.Close()
	}
	response, err := localHTTP().Get("http://127.0.0.1:9001/health")
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Relay 健康检查失败")
	}
	return nil
}

var _ = json.Valid
