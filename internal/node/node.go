// Package node owns installation and menu operations for separate server roles.
package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"netgo/internal/securefile"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Runner func(context.Context, string, []string, []byte, []string) ([]byte, error)
type Manager struct {
	Role string
	Root string // Only set by tests; production owns fixed absolute paths.
	Run  Runner
	In   io.Reader
	Out  io.Writer
}

func (m Manager) path(path string) string {
	if m.Root != "" {
		return filepath.Join(m.Root, strings.TrimPrefix(path, "/"))
	}
	return path
}
func (m Manager) base() string {
	if m.Role == "vps" {
		return m.path("/opt/netgo-vps")
	}
	return m.path("/opt/netgo")
}
func (m Manager) etc() string {
	if m.Role == "vps" {
		return m.path("/etc/netgo-vps")
	}
	return m.path("/etc/netgo")
}
func (m Manager) command(name string, args ...string) ([]byte, error) {
	return m.commandInput(nil, name, args...)
}
func (m Manager) commandInput(input []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	env := append(os.Environ(), "LC_ALL=C", "NB_STATE_DIR="+m.path("/var/lib/netgo-router"))
	var output []byte
	var err error
	if m.Run != nil {
		output, err = m.Run(ctx, name, args, input, env)
	} else {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(string(input))
		output, err = cmd.CombinedOutput()
	}
	if err != nil {
		if name == "systemctl" && len(args) > 0 && (args[0] == "is-active" || args[0] == "is-enabled") {
			return output, err
		}
		_ = m.write(filepath.Join(m.base(), "command-error.txt"), output, 0600)
		return nil, fmt.Errorf("%s 执行失败，诊断保存在 %s", filepath.Base(name), filepath.Join(m.base(), "command-error.txt"))
	}
	return output, nil
}
func noLinks(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing symlink path")
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
func (m Manager) write(path string, b []byte, mode os.FileMode) error {
	if err := noLinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	if err := securefile.Write(path, b, mode); err != nil {
		return err
	}
	if m.Root == "" && mode == 0640 {
		group := "netgo"
		if m.Role == "vps" {
			group = "netgo-vps"
		}
		g, err := user.LookupGroup(group)
		if err != nil {
			return err
		}
		gid, err := strconv.Atoi(g.Gid)
		if err != nil {
			return err
		}
		if err = os.Chown(path, 0, gid); err != nil {
			return err
		}
		return os.Chown(filepath.Dir(path), 0, gid)
	}
	return nil
}
func (m Manager) json(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return m.write(path, append(b, '\n'), 0600)
}
func readJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(value)
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func sha(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("需要常规文件")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (m Manager) print(format string, args ...any) {
	out := m.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, format+"\n", args...)
}
func (m Manager) active(unit string) bool {
	_, err := m.command("systemctl", "is-active", "--quiet", unit)
	return err == nil
}
func (m Manager) service(action string, units ...string) error {
	for _, unit := range units {
		if _, err := m.command("systemctl", action, unit); err != nil {
			return err
		}
	}
	return nil
}
func wait(description string, check func() error) error {
	end := time.Now().Add(45 * time.Second)
	for {
		if err := check(); err == nil {
			return nil
		}
		if time.Now().After(end) {
			return fmt.Errorf("等待 %s 超时", description)
		}
		time.Sleep(time.Second)
	}
}
func checkPorts(tcp, udp []int) error {
	for _, number := range tcp {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", number))
		if err != nil {
			return fmt.Errorf("TCP 端口 %d 已被占用", number)
		}
		l.Close()
	}
	for _, number := range udp {
		l, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", number))
		if err != nil {
			return fmt.Errorf("UDP 端口 %d 已被占用", number)
		}
		l.Close()
	}
	return nil
}
func localHTTP() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
