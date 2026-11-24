// Package desktopinstall owns the Linux desktop network component, separately
// from LAN server and VPS node installation.
package desktopinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"netgo/internal/securefile"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const base = "/opt/netgo-client"
const unit = "/etc/systemd/system/netgo-client-network.service"
const socket = "/run/netgo-client/netbird.sock"
const ownerTag = "NetGo desktop network v1"

type installation struct {
	Owner       string
	UID         uint
	Uninstalled bool
	Initialized bool
	Files       map[string]string
}

func noLinks(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("network installation path is a symlink")
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func command(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}
func save(state installation) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return securefile.Write(base+"/installation.json", data, 0600)
}
func verify(state installation, uid uint) error {
	if state.Owner != ownerTag || state.UID != uid {
		return errors.New("network component belongs to another installation or user")
	}
	for path, hash := range state.Files {
		if path != unit && !strings.HasPrefix(path, base+"/bin/") {
			return errors.New("invalid network ownership path")
		}
		if err := noLinks(path); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if state.Uninstalled && path == unit && os.IsNotExist(err) {
			continue
		}
		if err != nil || digest(data) != hash {
			return fmt.Errorf("network component changed outside NetGo: %s", path)
		}
	}
	return nil
}

// Run is invoked by the desktop UI through the system's administrator prompt.
func Run(action, source string, uid uint) (result error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || uid == 0 {
		return errors.New("Linux network installation needs administrator privileges and a desktop user")
	}
	if _, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err != nil {
		return errors.New("desktop user does not exist")
	}
	if action == "socket" {
		for i := 0; i < 100; i++ {
			info, err := os.Lstat(socket)
			if err == nil && info.Mode()&os.ModeSocket != 0 {
				if info.Mode().Perm() != 0600 {
					if err = os.Chmod(socket, 0600); err != nil {
						return err
					}
				}
				if err = os.Chown(socket, int(uid), -1); err != nil {
					return err
				}
				return nil
			}
			if err == nil {
				return errors.New("network IPC path is not a socket")
			}
			time.Sleep(100 * time.Millisecond)
		}
		return errors.New("network IPC did not become ready")
	}
	if action != "install" && action != "uninstall" {
		return errors.New("invalid network maintenance action")
	}
	if err := noLinks(base); err != nil {
		return err
	}
	if err := noLinks(unit); err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return err
	}
	unlock, err := lockNetwork()
	if err != nil {
		return err
	}
	defer unlock()
	state := installation{Owner: ownerTag, UID: uid, Files: map[string]string{}}
	if data, err := os.ReadFile(base + "/installation.json"); err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
		if err = verify(state, uid); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if _, e := os.Stat(unit); !os.IsNotExist(e) {
			return errors.New("unrecognized existing network service")
		}
		if entries, e := os.ReadDir(base); e == nil {
			for _, entry := range entries {
				if entry.Name() != ".network.lock" {
					return errors.New("unrecognized existing network directory")
				}
			}
		}
	}
	if action == "uninstall" {
		if len(state.Files) == 0 {
			return errors.New("network component is not installed")
		}
		if _, e := os.Stat(unit); e == nil {
			if err := command("systemctl", "disable", "--now", "netgo-client-network"); err != nil {
				return err
			}
		}
		// Record the stopped state before removal so interrupted uninstall can
		// resume even when the service file has already gone.
		state.Uninstalled = true
		if err := save(state); err != nil {
			return err
		}
		if err := os.Remove(unit); err != nil && !os.IsNotExist(err) {
			return err
		}
		capabilities, err := exec.Command("getcap", base+"/bin/netgo-client-service").Output()
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(capabilities)) != "" {
			if err := command("setcap", "-r", base+"/bin/netgo-client-service"); err != nil {
				return err
			}
		}
		return command("systemctl", "daemon-reload")
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	if err = noLinks(source); err != nil {
		return err
	}
	sums, err := os.ReadFile(filepath.Join(source, "SHA256SUMS"))
	if err != nil {
		return errors.New("verified desktop package required")
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			hashes[fields[1]] = fields[0]
		}
	}
	payload := map[string][]byte{}
	for _, name := range []string{"netbird", "netgo-client-service"} {
		path := filepath.Join(source, name)
		if err = noLinks(path); err != nil {
			return err
		}
		data, e := os.ReadFile(path)
		if e != nil || len(data) < 20 || string(data[:5]) != "\x7fELF\x02" || data[18] != 62 || data[19] != 0 || digest(data) != hashes[name] {
			return errors.New("desktop program checksum or architecture mismatch: " + name)
		}
		payload[name] = data
	}
	if _, err = exec.LookPath("setcap"); err != nil {
		if err = command("apt-get", "install", "-y", "--no-upgrade", "--no-install-recommends", "libcap2-bin"); err != nil {
			return err
		}
	}
	oldState := state
	oldState.Files = map[string]string{}
	previous := map[string][]byte{}
	for path, hash := range state.Files {
		oldState.Files[path] = hash
		data, e := os.ReadFile(path)
		if e != nil && !(state.Uninstalled && path == unit && os.IsNotExist(e)) {
			return e
		}
		if e == nil {
			previous[path] = data
		}
	}
	wasActive := len(state.Files) > 0 && exec.Command("systemctl", "is-active", "--quiet", "netgo-client-network").Run() == nil
	wasEnabled := len(state.Files) > 0 && exec.Command("systemctl", "is-enabled", "--quiet", "netgo-client-network").Run() == nil
	if len(state.Files) > 0 && !state.Uninstalled {
		if err = command("systemctl", "stop", "netgo-client-network"); err != nil {
			return err
		}
	}
	fresh := len(state.Files) == 0
	committed := false
	created := map[string]string{}
	defer func() {
		if committed {
			return
		}
		if _, ok := created[unit]; ok {
			result = errors.Join(result, command("systemctl", "disable", "--now", "netgo-client-network"))
		}
		for path, hash := range created {
			if data, e := os.ReadFile(path); e == nil && digest(data) == hash {
				if old, ok := previous[path]; ok {
					mode := os.FileMode(0755)
					if path == unit {
						mode = 0644
					}
					result = errors.Join(result, securefile.Write(path, old, mode))
				} else {
					result = errors.Join(result, os.Remove(path))
				}
			} else if e == nil {
				result = errors.Join(result, fmt.Errorf("rollback preserved externally changed file: %s", path))
			}
		}
		if fresh {
			_ = os.Remove(base + "/bin")
			// Daemon-created keys/configuration are mutable state. Preserve their
			// ownership record so a failed first start can be retried safely.
			oldState.Initialized = state.Initialized
			oldState.Uninstalled = true
			result = errors.Join(result, save(oldState))
		} else {
			result = errors.Join(result, save(oldState))
			if !oldState.Uninstalled {
				result = errors.Join(result, command("setcap", "cap_net_raw=ep", base+"/bin/netgo-client-service"))
			}
		}
		result = errors.Join(result, command("systemctl", "daemon-reload"))
		if wasEnabled {
			result = errors.Join(result, command("systemctl", "enable", "netgo-client-network"))
		}
		if wasActive {
			result = errors.Join(result, command("systemctl", "start", "netgo-client-network"))
		}
	}()
	if err = os.MkdirAll(base+"/bin", 0755); err != nil {
		return err
	}
	if err = noLinks("/var/lib/netgo-client"); err != nil {
		return err
	}
	if err = os.MkdirAll("/var/lib/netgo-client", 0700); err != nil {
		return err
	}
	if !state.Initialized {
		entries, e := os.ReadDir("/var/lib/netgo-client")
		if e != nil {
			return e
		}
		if len(entries) > 0 {
			return errors.New("unrecognized network state directory")
		}
		if err = securefile.Write("/var/lib/netgo-client/config.json", []byte(`{"DisableAutoConnect":true,"WgIface":"ngc0","WgPort":0,"ManagementURL":{"Scheme":"https","Host":"127.0.0.1:1"}}`), 0600); err != nil {
			return err
		}
		state.Initialized = true
	}
	for name, data := range payload {
		path := base + "/bin/" + name
		if err = securefile.Write(path, data, 0755); err != nil {
			return err
		}
		state.Files[path] = digest(data)
		created[path] = digest(data)
	}
	if err = command("setcap", "cap_net_raw=ep", base+"/bin/netgo-client-service"); err != nil {
		return err
	}
	service := fmt.Sprintf("# Managed by %s\n[Unit]\nDescription=NetGo desktop network\nAfter=network-online.target\n[Service]\nEnvironment=NB_STATE_DIR=/var/lib/netgo-client\nEnvironment=NB_LAZY_CONN=false\nExecStart=%s/bin/netbird --config /var/lib/netgo-client/config.json --daemon-addr unix://%s --log-file console service run\nExecStartPost=%s/bin/netgo-client-service --network socket --owner %d\nRuntimeDirectory=netgo-client\nRuntimeDirectoryMode=0755\nRestart=on-failure\nRestartSec=3\nCapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_CHOWN CAP_DAC_OVERRIDE\nNoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nReadWritePaths=/var/lib/netgo-client /run/netgo-client\n[Install]\nWantedBy=multi-user.target\n", ownerTag, base, socket, base, uid)
	if err = securefile.Write(unit, []byte(service), 0644); err != nil {
		return err
	}
	state.Files[unit] = digest([]byte(service))
	created[unit] = digest([]byte(service))
	state.Uninstalled = false
	if err = save(state); err != nil {
		return err
	}
	if err = command("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err = command("systemctl", "enable", "--now", "netgo-client-network"); err != nil {
		return err
	}
	committed = true
	return nil
}
