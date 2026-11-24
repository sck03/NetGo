package node

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

func checkSystemUser(name string) error {
	raw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || fields[0] != name {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil || uid <= 0 || uid >= 1000 || (fields[6] != "/usr/sbin/nologin" && fields[6] != "/bin/false") {
			return errors.New("已有非受管的同名系统账号")
		}
		return nil
	}
	return nil
}
func (m Manager) dataDirectory(path, account string) error {
	if err := noLinks(path); err != nil {
		return err
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		marker, e := os.ReadFile(filepath.Join(path, ".owner"))
		if e != nil || string(marker) != Owner {
			return errors.New("数据目录含未识别文件，拒绝接管")
		}
	}
	if err := os.MkdirAll(path, 0750); err != nil {
		return err
	}
	if err := m.write(filepath.Join(path, ".owner"), []byte(Owner), 0600); err != nil {
		return err
	}
	if m.Root != "" {
		return nil
	}
	u, err := user.Lookup(account)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	if err = os.Chown(path, uid, gid); err != nil {
		return err
	}
	return os.Chmod(path, 0750)
}
