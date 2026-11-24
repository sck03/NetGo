package node

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"netgo/internal/deployment"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type NodeSecrets struct {
	Relay string
	FRP   string
}
type Bundle struct {
	Schema        int               `json:"schema"`
	Owner         string            `json:"owner"`
	SiteID        string            `json:"site_id"`
	ControlOrigin string            `json:"control_origin"`
	Auxiliary     site.Auxiliary    `json:"auxiliary"`
	Node          deployment.Node   `json:"node"`
	Secrets       NodeSecrets       `json:"secrets"`
	Files         map[string]string `json:"files"`
}
type BundleData struct {
	Info  Bundle
	Files map[string][]byte
}

func (m Manager) ExportVPS(c site.Config, certificates map[string][]byte, path string) error {
	if len(c.Auxiliaries) != 1 {
		return errors.New("需要先配置一个 VPS 辅助节点")
	}
	var secrets deployment.Secrets
	if err := readJSON(filepath.Join(m.etc(), "rendered/secrets.json"), &secrets); err != nil {
		return err
	}
	rendered, err := deployment.Render(c, secrets)
	if err != nil {
		return err
	}
	data := BundleData{Info: Bundle{Schema: 2, Owner: "NetGo VPS bundle", SiteID: c.Site.ID, ControlOrigin: c.Site.Origin, Auxiliary: c.Auxiliaries[0], Node: *rendered.Auxiliary, Secrets: NodeSecrets{secrets.Relay, secrets.FRP}, Files: map[string]string{}}, Files: map[string][]byte{}}
	for name, b := range rendered.Files {
		if strings.HasPrefix(name, "auxiliary/") {
			data.Files[name] = b
		}
	}
	for _, name := range []string{"relay.crt", "relay.key", "tunnel.crt", "tunnel.key", "ca.crt"} {
		if b := certificates[name]; len(b) > 0 {
			data.Files["tls/"+name] = b
		}
	}
	return writeBundle(path, data)
}
func writeBundle(path string, data BundleData) (err error) {
	for name, b := range data.Files {
		data.Info.Files[name] = digest(b)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if e := file.Close(); err == nil {
			err = e
		}
	}()
	z := zip.NewWriter(file)
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	names := []string{}
	for name := range data.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, e := z.Create(name)
		if e != nil {
			return e
		}
		if _, e = entry.Write(data.Files[name]); e != nil {
			return e
		}
	}
	meta, e := z.Create("vps.json")
	if e != nil {
		return e
	}
	if e = json.NewEncoder(meta).Encode(data.Info); e != nil {
		return e
	}
	return z.Close()
}
func ReadBundle(path string) (BundleData, error) {
	data := BundleData{Files: map[string][]byte{}}
	z, err := zip.OpenReader(path)
	if err != nil {
		return data, err
	}
	defer z.Close()
	if len(z.File) > 20 {
		return data, errors.New("配置包包含过多文件")
	}
	size := uint64(0)
	seen := map[string]bool{}
	for _, entry := range z.File {
		size += entry.UncompressedSize64
		if size > 2<<20 || seen[entry.Name] || entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return data, errors.New("配置包包含重复、链接或超大文件")
		}
		seen[entry.Name] = true
		allowed := entry.Name == "vps.json" || entry.Name == "auxiliary/frps.toml" || entry.Name == "auxiliary/relay.env" || entry.Name == "auxiliary/haproxy.cfg" || entry.Name == "auxiliary/relay-port.nft"
		for _, name := range []string{"relay.crt", "relay.key", "tunnel.crt", "tunnel.key", "ca.crt"} {
			allowed = allowed || entry.Name == "tls/"+name
		}
		if !allowed {
			return data, errors.New("配置包含非运行文件：" + entry.Name)
		}
		stream, e := entry.Open()
		if e != nil {
			return data, e
		}
		b, e := io.ReadAll(io.LimitReader(stream, 2<<20))
		stream.Close()
		if e != nil {
			return data, e
		}
		if entry.Name == "vps.json" {
			if e = site.Decode(strings.NewReader(string(b)), &data.Info); e != nil {
				return data, e
			}
		} else {
			data.Files[entry.Name] = b
		}
	}
	if err = data.Validate(); err != nil {
		return data, err
	}
	return data, nil
}
func (data BundleData) Validate() error {
	info := data.Info
	if info.Schema != 2 || info.Owner != "NetGo VPS bundle" || !site.ValidID(info.SiteID) {
		return errors.New("不是 VPS 专用配置包")
	}
	if err := info.Auxiliary.Validate(); err != nil {
		return err
	}
	// Re-render from the public endpoints and node-scoped secrets; arbitrary
	// frps/HAProxy directives are never accepted from an import archive.
	c := site.Config{Site: site.Profile{Protocol: 2, ID: info.SiteID, Origin: info.ControlOrigin, Candidates: []site.Candidate{{ID: "local", Kind: "direct", Address: "127.0.0.1:10443"}, {ID: info.Auxiliary.ID, Kind: "auxiliary", Address: fmt.Sprintf("%s:%d", info.Auxiliary.Host, info.Auxiliary.PublicControlPort())}}, LocalRelay: "local.netgo.invalid", AuxiliaryRelay: info.Auxiliary.Relay}, Listen: "127.0.0.1:18081", Database: "/var/lib/netgo/netgo.db", ManagementURL: "http://127.0.0.1:18080", ManagementTokenFile: "/etc/netgo/token", NativeSecretFile: "/etc/netgo/native.secret", HelperSocket: "/run/netgo-helper/helper.sock", Interface: "wt0", LANInterface: "eth0", OverlayCIDR: "100.96.0.0/16", LeaseSeconds: 120, Auxiliaries: []site.Auxiliary{info.Auxiliary}, Deployment: site.Deployment{Mode: "diagnostic", LocalRelayPort: 18443, LocalSTUNPort: 13478, ManagementHealthPort: 19000, ManagementMetricsPort: 19090, RelayHealthPort: 19001, RelayMetricsPort: 19091, CaddyAdminPort: 12019}}
	// Local listeners are placeholders outside the auxiliary role. Keep them
	// distinct from the LAN control endpoint to validate any user-chosen port.
	_, control := endpointForNode(info.ControlOrigin)
	taken := map[int]bool{control: true}
	local := []*int{&c.Deployment.LocalRelayPort, &c.Deployment.LocalSTUNPort, &c.Deployment.ManagementHealthPort, &c.Deployment.ManagementMetricsPort, &c.Deployment.RelayHealthPort, &c.Deployment.RelayMetricsPort, &c.Deployment.CaddyAdminPort}
	next := 30000
	for _, p := range local {
		for taken[next] {
			next++
		}
		*p = next
		taken[next] = true
		next++
	}
	for taken[next] {
		next++
	}
	c.Listen = fmt.Sprintf("127.0.0.1:%d", next)
	taken[next] = true
	next++
	for taken[next] {
		next++
	}
	c.ManagementURL = fmt.Sprintf("http://127.0.0.1:%d", next)
	secrets := deployment.Secrets{Relay: info.Secrets.Relay, FRP: info.Secrets.FRP, Native: strings.Repeat("a", 64), Store: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
	rendered, err := deployment.Render(c, secrets)
	if err != nil {
		return err
	}
	nodeBytes, _ := json.Marshal(info.Node)
	expectedBytes, _ := json.Marshal(rendered.Auxiliary)
	if string(nodeBytes) != string(expectedBytes) {
		return errors.New("节点端口元数据不匹配")
	}
	expectedFiles := map[string]bool{}
	for name, b := range rendered.Files {
		if strings.HasPrefix(name, "auxiliary/") {
			expectedFiles[name] = true
			if string(data.Files[name]) != string(b) {
				return errors.New("节点配置与已审查模板不一致")
			}
		}
	}
	tlsFiles := map[string][]byte{}
	for _, name := range []string{"relay.crt", "relay.key", "tunnel.crt", "tunnel.key"} {
		if len(data.Files["tls/"+name]) == 0 {
			return errors.New("节点证书不完整")
		}
		expectedFiles["tls/"+name] = true
		tlsFiles[name] = data.Files["tls/"+name]
	}
	if ca := data.Files["tls/ca.crt"]; len(ca) > 0 {
		expectedFiles["tls/ca.crt"] = true
		tlsFiles["ca.crt"] = ca
	}
	if len(data.Files) != len(expectedFiles) || len(info.Files) != len(data.Files) {
		return errors.New("配置包含多余或缺失文件")
	}
	for name, b := range data.Files {
		if !expectedFiles[name] || digest(b) != info.Files[name] {
			return errors.New("节点配置摘要不匹配")
		}
	}
	u, _ := url.Parse(info.Auxiliary.Relay)
	return validateCertificates(map[string]string{"relay": u.Hostname(), "tunnel": info.Auxiliary.TunnelHost}, tlsFiles)
}
func endpointForNode(origin string) (string, int) {
	u, _ := url.Parse(origin)
	return u.Hostname(), deploymentPort(u.Port(), 443)
}
