package node

import (
	"errors"
	"fmt"
	"io/fs"
	"netgo/deploy/linux"
	"netgo/internal/platform"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const Owner = "NetGo node v2"

type Installation struct {
	Owner  string            `json:"owner"`
	Role   string            `json:"role"`
	Status string            `json:"status"`
	Files  map[string]string `json:"files"`
	Links  map[string]string `json:"links"`
	Units  []string          `json:"units"`
}

var ServerProducts = []string{"netgo-server", "netgo-helper", "netgoctl", "netgo-ddns"}
var ServerUpstream = []string{"netbird-server", "netbird", "netbird-relay", "caddy", "frpc"}

func (m Manager) installation() (Installation, error) {
	var state Installation
	err := readJSON(filepath.Join(m.base(), "installation.json"), &state)
	return state, err
}
func (m Manager) verifyInstalled(state Installation) error {
	if state.Owner != Owner || state.Role != m.Role {
		return errors.New("未知安装归属，保留现有文件")
	}
	for path, expected := range state.Files {
		if filepath.Clean(path) != path {
			return errors.New("非法归属路径")
		}
		if err := noLinks(path); err != nil {
			return err
		}
		if !strings.HasPrefix(path, m.base()+string(os.PathSeparator)) && !strings.HasPrefix(path, m.path("/etc/systemd/system")+string(os.PathSeparator)) {
			return errors.New("非法归属路径")
		}
		value, err := sha(path)
		if err != nil || value != expected {
			return fmt.Errorf("受管文件被外部修改：%s", path)
		}
	}
	for path, target := range state.Links {
		actual, err := os.Readlink(path)
		if err != nil || actual != target {
			return fmt.Errorf("受管快捷命令被修改：%s", path)
		}
	}
	return nil
}
func (m Manager) saveInstallation(state Installation) error {
	return m.json(filepath.Join(m.base(), "installation.json"), state)
}
func (m Manager) recoverFailedInstallation(state Installation) error {
	if state.Owner != Owner || state.Role != m.Role {
		return errors.New("未知安装归属，保留现有文件")
	}
	for path, hash := range state.Files {
		if filepath.Clean(path) != path || (!strings.HasPrefix(path, m.base()+string(os.PathSeparator)) && !strings.HasPrefix(path, m.path("/etc/systemd/system")+string(os.PathSeparator))) {
			return errors.New("非法归属路径")
		}
		if err := noLinks(path); err != nil {
			return err
		}
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		}
		actual, err := sha(path)
		if err != nil || actual != hash {
			return errors.New("失败安装的残留文件被外部修改，保留现场")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	short, long := "ng", "netgo-serverctl"
	if m.Role == "vps" {
		short, long = "ngv", "netgo-vpsctl"
	}
	for path, target := range state.Links {
		if path != m.path("/usr/local/bin/"+short) && path != m.path("/usr/local/bin/"+long) {
			return errors.New("非法快捷命令归属")
		}
		actual, err := os.Readlink(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || actual != target {
			return errors.New("失败安装的快捷命令被外部修改")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	for _, name := range []string{"current", "upstream", "bin"} {
		path := filepath.Join(m.base(), name)
		if err := noLinks(path); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return errors.New("安装残留目录有未识别内容，保留现场")
		}
	}
	return os.Remove(filepath.Join(m.base(), "installation.json"))
}
func (m Manager) Install(source string) (result error) {
	if source == "" {
		return errors.New("需要下载并校验的完整平台包，或 --source 本地完整包")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if err = noLinks(source); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(m.base(), "install.manifest")); err == nil {
		return errors.New("发现旧式安装归属，请保留现有环境并使用干净的测试系统")
	}
	if old, e := m.installation(); e == nil && old.Status == "failed" {
		if err = m.recoverFailedInstallation(old); err != nil {
			return err
		}
	}
	if old, e := m.installation(); e == nil {
		if old.Owner != Owner || old.Role != m.Role {
			return errors.New("未知安装归属，保留现有文件")
		}
		if old.Status != "uninstalled" {
			if e = m.verifyInstalled(old); e != nil {
				return e
			}
			m.print("程序已安装。使用菜单启动或修改设置。")
			return nil
		}
		for path, expected := range old.Files {
			value, e := sha(path)
			if e != nil || value != expected {
				return errors.New("保留的程序文件已变化")
			}
		}
		for path, target := range old.Links {
			if _, e := os.Lstat(path); !os.IsNotExist(e) {
				return errors.New("快捷命令路径已被其他程序使用")
			}
			if e = os.Symlink(target, path); e != nil {
				return e
			}
		}
		old.Units = nil
		if e = m.installUnits(&old); e != nil {
			return e
		}
		old.Status = "installed"
		if e = m.saveInstallation(old); e != nil {
			return e
		}
		_, e = m.command("systemctl", "daemon-reload")
		return e
	}
	if entries, e := os.ReadDir(m.base()); e == nil {
		for _, entry := range entries {
			if entry.Name() != ".install.lock" && entry.Name() != "command-error.txt" {
				return errors.New("安装目录存在未识别文件，拒绝覆盖")
			}
		}
	}
	files := map[string]string{}
	if m.Role == "server" {
		release := filepath.Join(source, "dist/linux-"+runtime.GOARCH)
		upstream := filepath.Join(source, "dist/upstream-linux-"+runtime.GOARCH)
		for _, name := range ServerProducts {
			files[filepath.Join(m.base(), "current", name)] = filepath.Join(release, name)
		}
		for _, name := range ServerUpstream {
			files[filepath.Join(m.base(), "upstream", name)] = filepath.Join(upstream, name)
		}
	} else {
		for _, name := range []string{"frps", "netbird-relay", "netgo-vpsctl"} {
			files[filepath.Join(m.base(), "bin", name)] = filepath.Join(source, "dist/vps/bin", name)
		}
	}
	for _, src := range files {
		info, e := os.Lstat(src)
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("缺少平台运行程序：" + src)
		}
		if err := platform.VerifyELF(src, runtime.GOARCH); err != nil {
			return err
		}
	}
	if m.Root == "" {
		if err = m.dependencies(); err != nil {
			return err
		}
		group := "netgo"
		if m.Role == "vps" {
			group = "netgo-vps"
		}
		if err = checkSystemUser(group); err != nil {
			return err
		}
		if _, e := m.command("getent", "group", group); e != nil {
			if _, e = m.command("groupadd", "--system", group); e != nil {
				return e
			}
		}
		if _, e := m.command("id", group); e != nil {
			if _, e = m.command("useradd", "--system", "--gid", group, "--home-dir", m.path("/var/lib/"+group), "--shell", "/usr/sbin/nologin", group); e != nil {
				return e
			}
		}
	}
	state := Installation{Owner: Owner, Role: m.Role, Status: "installed", Files: map[string]string{}, Links: map[string]string{}}
	committed := false
	defer func() {
		if !committed {
			for path := range state.Links {
				_ = os.Remove(path)
			}
			for path := range state.Files {
				_ = os.Remove(path)
			}
			_ = m.saveInstallation(Installation{Owner: Owner, Role: m.Role, Status: "failed", Files: state.Files, Links: state.Links})
		}
	}()
	for dst, src := range files {
		if _, e := os.Lstat(dst); !os.IsNotExist(e) {
			return errors.New("现有程序路径：" + dst)
		}
		b, e := os.ReadFile(src)
		if e != nil {
			return e
		}
		if e = m.write(dst, b, 0755); e != nil {
			return e
		}
		if e = os.Chmod(filepath.Dir(dst), 0755); e != nil {
			return e
		}
		state.Files[dst] = digest(b)
	}
	if err = m.installUnits(&state); err != nil {
		return err
	}
	binary := filepath.Join(m.base(), "current/netgoctl")
	short := "ng"
	long := "netgo-serverctl"
	if m.Role == "vps" {
		binary = filepath.Join(m.base(), "bin/netgo-vpsctl")
		short = "ngv"
		long = "netgo-vpsctl"
	}
	for _, name := range []string{short, long} {
		link := m.path("/usr/local/bin/" + name)
		if _, e := os.Lstat(link); !os.IsNotExist(e) {
			return errors.New("快捷命令已被其他程序使用：" + name)
		}
		if err = os.MkdirAll(filepath.Dir(link), 0755); err != nil {
			return err
		}
		if err = os.Symlink(binary, link); err != nil {
			return err
		}
		state.Links[link] = binary
	}
	if err = m.saveInstallation(state); err != nil {
		return err
	}
	if _, err = m.command("systemctl", "daemon-reload"); err != nil {
		return err
	}
	committed = true
	m.print("程序已安装。快捷命令：%s。首次配置将在菜单中完成。", short)
	return nil
}
func (m Manager) installUnits(state *Installation) error {
	if m.Role == "vps" {
		return nil
	}
	entries, err := fs.ReadDir(linux.Units, ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".service") {
			continue
		}
		path := m.path("/etc/systemd/system/" + entry.Name())
		if _, e := os.Lstat(path); !os.IsNotExist(e) {
			return errors.New("已有 systemd 单元：" + entry.Name())
		}
		b, err := linux.Units.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		if err = m.write(path, b, 0644); err != nil {
			return err
		}
		state.Files[path] = digest(b)
		state.Units = append(state.Units, strings.TrimSuffix(entry.Name(), ".service"))
	}
	sort.Strings(state.Units)
	return nil
}
func (m Manager) dependencies() error {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = strings.Trim(value, "\"")
		}
	}
	if values["ID"] != "debian" || (values["VERSION_ID"] != "12" && values["VERSION_ID"] != "13") {
		return errors.New("当前支持 Debian 12/13 amd64/arm64")
	}
	if m.Role == "vps" {
		return nil
	}
	missing := []string{}
	for binary, pkg := range map[string]string{"nft": "nftables", "conntrack": "conntrack", "ip": "iproute2"} {
		if _, e := execLookPath(binary); e != nil {
			missing = append(missing, pkg)
		}
	}
	if len(missing) > 0 {
		if _, err = m.command("apt-get", "update"); err != nil {
			return err
		}
		_, err = m.command("apt-get", append([]string{"install", "-y", "--no-upgrade", "--no-install-recommends"}, missing...)...)
		return err
	}
	return nil
}
func (m Manager) Uninstall() error {
	state, err := m.installation()
	if err != nil {
		return err
	}
	if state.Status == "uninstalled" {
		m.print("已经卸载，保留数据未改动。")
		return nil
	}
	if err = m.verifyInstalled(state); err != nil {
		return err
	}
	for i := len(state.Units) - 1; i >= 0; i-- {
		if err = m.service("disable", state.Units[i]); err != nil {
			return err
		}
		if err = m.service("stop", state.Units[i]); err != nil {
			return err
		}
	}
	if m.Role == "server" {
		if err = m.NetworkRemove(); err != nil {
			return err
		}
		if err = m.RestoreForwarding(); err != nil {
			return err
		}
		for _, table := range []string{"netgo", "netgo_relay"} {
			if err = m.removeTable(table); err != nil {
				return err
			}
		}
	}
	for path := range state.Files {
		if strings.HasPrefix(path, m.path("/etc/systemd/system")+string(os.PathSeparator)) {
			if err = os.Remove(path); err != nil {
				return err
			}
			delete(state.Files, path)
		}
	}
	for path := range state.Links {
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	state.Status = "uninstalled"
	if err = m.saveInstallation(state); err != nil {
		return err
	}
	_, err = m.command("systemctl", "daemon-reload")
	m.print("已卸载受管服务和快捷命令，保留配置、证书和数据。")
	return err
}
func execLookPath(name string) (string, error) { return exec.LookPath(name) }
