package node

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"netgo/internal/deployment"
	"netgo/internal/model"
	"netgo/internal/platform"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func Main(role string, args []string) error {
	action := "menu"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("NetGo "+role, flag.ContinueOnError)
	source := flags.String("source", "", "已校验并解压的平台包目录")
	packageFile := flags.String("package", "", "本地平台安装 ZIP")
	packageSHA := flags.String("sha256", "", "本地安装 ZIP 的可信 SHA256")
	version := flags.String("version", "latest", "Release 版本")
	config := flags.String("config", "/etc/netgo/site.json", "站点配置文件")
	bundle := flags.String("bundle", "", "VPS 配置 ZIP")
	tls := flags.String("tls-dir", "", "证书目录")
	privateCA := flags.Bool("private-ca", false, "生成站点私有 CA")
	email := flags.String("admin-email", "", "管理员邮箱")
	alias := flags.String("admin-alias", "admin", "管理员用户名")
	passwordStdin := flags.Bool("password-stdin", false, "从 stdin 读取密码")
	only := flags.Bool("install-only", false, "仅安装程序")
	yes := flags.Bool("yes", false, "确认卸载受管服务")
	plan := flags.Bool("plan", false, "只显示计划")
	output := flags.String("output", "", "新输出路径")
	secretsFile := flags.String("secrets", "", "已有部署密钥 JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("存在多余参数")
	}
	if *plan {
		fmt.Printf("Plan: role=%s action=%s; Go maintenance, no Python runtime; owned files and services only; no changes made.\n", role, action)
		return nil
	}
	if action == "version" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"version": model.Version, "protocol": model.Protocol, "schema": model.Schema, "os": runtime.GOOS, "arch": runtime.GOARCH, "role": role})
	}
	m := Manager{Role: role, In: os.Stdin, Out: os.Stdout}
	if action == "render" {
		c, err := site.Load(*config)
		if err != nil {
			return err
		}
		var secrets deployment.Secrets
		if *secretsFile != "" {
			if err = readJSON(*secretsFile, &secrets); err != nil {
				return err
			}
		} else {
			secrets, err = deployment.NewSecrets()
			if err != nil {
				return err
			}
		}
		rendered, err := deployment.Render(c, secrets)
		if err != nil {
			return err
		}
		if *output == "" {
			return errors.New("需要 --output")
		}
		if _, err = os.Stat(*output); !os.IsNotExist(err) {
			return errors.New("输出目录已存在")
		}
		for name, b := range rendered.Files {
			if err = m.write(filepath.Join(*output, name), b, 0600); err != nil {
				return err
			}
		}
		return nil
	}
	if runtime.GOOS != "linux" || !platform.Supported(runtime.GOARCH) || os.Geteuid() != 0 {
		return errors.New("请以 root 在 Linux amd64/arm64 运行节点安装/维护")
	}
	if action == "network-apply" || action == "network-remove" {
		unlock, err := lock("/run/netgo-bypass.lock")
		if err != nil {
			return err
		}
		defer unlock()
		if action == "network-remove" {
			return m.NetworkRemove()
		}
		c, err := m.config()
		if err != nil {
			return err
		}
		return m.ApplyNetwork(&c)
	}
	if action == "redirect-remove" {
		table := "netgo_relay"
		if role == "vps" {
			table = "netgo_vps_relay"
		}
		return m.removeTable(table)
	}
	if err := noLinks(m.base()); err != nil {
		return err
	}
	if err := os.MkdirAll(m.base(), 0755); err != nil {
		return err
	}
	unlock, err := lock(filepath.Join(m.base(), ".install.lock"))
	if err != nil {
		return errors.New("另一个安装或维护操作正在运行")
	}
	defer unlock()
	switch action {
	case "menu":
		return m.Menu()
	case "install":
		if *source == "" {
			var cleanup func()
			if *packageFile != "" {
				*source, cleanup, err = stagePackage(*packageFile, *packageSHA)
			} else {
				*source, cleanup, err = m.releasePackage(*version)
			}
			if err != nil {
				return err
			}
			defer cleanup()
		} else if *packageFile != "" {
			return errors.New("安装来源参数冲突")
		}
		if err = m.Install(*source); err != nil {
			return err
		}
		if *only {
			return nil
		}
		if role == "vps" {
			if *bundle != "" {
				data, err := ReadBundle(*bundle)
				if err != nil {
					return err
				}
				return m.ConfigureVPS(data)
			}
			return m.Menu()
		}
		if *config != "/etc/netgo/site.json" || *passwordStdin {
			break
		}
		return m.Menu()
	case "configure":
		if role == "vps" {
			if *bundle == "" {
				return m.Menu()
			}
			data, err := ReadBundle(*bundle)
			if err != nil {
				return err
			}
			return m.ConfigureVPS(data)
		}
		if *config == "/etc/netgo/site.json" && !*passwordStdin {
			return terminal{m, bufio.NewReader(os.Stdin)}.setup()
		}
	case "status":
		state, err := m.installation()
		if err != nil {
			return err
		}
		m.print("%s：%s", role, state.Status)
		if role == "vps" && state.Status == "active" {
			return m.DoctorVPS()
		}
		return nil
	case "doctor":
		if role == "vps" {
			return m.DoctorVPS()
		}
		return m.StartServer()
	case "uninstall":
		if !*yes {
			return errors.New("确认卸载请使用菜单，或传 --yes")
		}
		return m.Uninstall()
	default:
		return errors.New("未知维护操作")
	}
	if role != "server" {
		return errors.New("不支持该角色操作")
	}
	c, err := site.Load(*config)
	if err != nil {
		return err
	}
	password := ""
	if *passwordStdin {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 75))
		if err != nil {
			return err
		}
		if len(b) >= 75 {
			clear(b)
			return errors.New("密码输入过长")
		}
		password = strings.TrimRight(string(b), "\r\n")
		clear(b)
	} else {
		password, err = terminal{m, bufio.NewReader(os.Stdin)}.password("管理员密码")
		if err != nil {
			return err
		}
	}
	return m.SetupServer(c, *email, *alias, password, *tls, *privateCA)
}
