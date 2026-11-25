package node

import (
	"errors"
	"net"
	"net/url"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (t terminal) auxiliaryMenu(c site.Config) error {
	t.m.print("VPS：1) 添加/修改地址端口  2) 导出节点配置包  3) 停用 VPS  4) 导入 VPS 新设置  0) 返回")
	selected, err := t.text("请选择", "0")
	if err != nil || selected == "0" {
		return err
	}
	if selected == "3" {
		c.Auxiliaries = nil
		c.Site.AuxiliaryRelay = ""
		list := []site.Candidate{}
		for _, candidate := range c.Site.Candidates {
			if candidate.Kind == "direct" {
				list = append(list, candidate)
			}
		}
		c.Site.Candidates = list
		return t.m.ApplyServerConfig(c, nil)
	}
	if selected == "1" || selected == "4" {
		a := site.Auxiliary{ID: "vps1", Mode: "diagnostic", TokenFile: "/etc/netgo/native.secret", ControlPort: 24443, TunnelPort: 27000}
		if len(c.Auxiliaries) > 0 {
			a = c.Auxiliaries[0]
		}
		if selected == "4" {
			path, e := t.text("VPS 导出的新设置 JSON", "")
			if e != nil {
				return e
			}
			var settings VPSSettings
			if e = readJSON(path, &settings); e != nil {
				return e
			}
			if settings.Owner != "NetGo VPS settings" || settings.SiteID != c.Site.ID {
				return errors.New("设置不属于本站点")
			}
			a = settings.Auxiliary
		} else {
			a, err = t.auxiliarySettings(a, len(c.Auxiliaries) == 0)
			if err != nil {
				return err
			}
		}
		if err = a.Validate(); err != nil {
			return err
		}
		c.Auxiliaries = []site.Auxiliary{a}
		list := []site.Candidate{}
		for _, candidate := range c.Site.Candidates {
			if candidate.Kind == "direct" {
				list = append(list, candidate)
			}
		}
		c.Site.Candidates = append(list, site.Candidate{ID: a.ID, Kind: "auxiliary", Address: net.JoinHostPort(a.Host, strconv.Itoa(a.PublicControlPort()))})
		c.Site.AuxiliaryRelay = a.Relay
		if err = t.m.ApplyServerConfig(c, nil); err != nil {
			return err
		}
		c, err = t.m.config()
		if err != nil {
			return err
		}
	} else if selected != "2" {
		return errors.New("无效选项")
	}
	if len(c.Auxiliaries) == 0 {
		return errors.New("需要先添加 VPS")
	}
	files, err := t.certificateFiles(c, "vps")
	if err != nil || files == nil {
		return err
	}
	if ca := string(files["ca.crt"]); ca != "" && !strings.Contains(c.Site.CA, strings.TrimSpace(ca)) {
		c.Site.CA += "\n" + ca
		if err = t.m.ApplyServerConfig(c, nil); err != nil {
			return err
		}
		c, err = t.m.config()
		if err != nil {
			return err
		}
	}
	path, err := t.text("VPS 配置包保存路径", "/root/NetGo-vps.zip")
	if err != nil {
		return err
	}
	if err = t.m.ExportVPS(c, files, path); err != nil {
		return err
	}
	t.m.print("VPS 专用配置包：%s。请私下复制到 VPS，在 ngv 菜单导入；包内包含节点私钥，不含站点 CA 私钥。", path)
	return nil
}

type VPSSettings struct {
	Owner     string
	SiteID    string
	Auxiliary site.Auxiliary
}

func (t terminal) vpsOperation(value string) error {
	switch value {
	case "import":
		path, err := t.text("LAN 导出的配对包位置", "/root/NetGo-vps.zip")
		if err != nil {
			return err
		}
		data, err := ReadBundle(path)
		if err != nil {
			return err
		}
		return t.m.ConfigureVPS(data)
	case "status":
		var info Bundle
		if err := readJSON(filepath.Join(t.m.etc(), "vps.json"), &info); err != nil {
			return err
		}
		t.m.print("站点：%s\n控制端口：%d\n隧道端口：%d\nRelay 端口：%d\nSTUN 端口：%d", info.SiteID, info.Node.ControlPort, info.Node.TunnelPort, info.Node.RelayPort, info.Node.STUNPort)
		return t.m.DoctorVPS()
	case "ports":
		path := filepath.Join(t.m.etc(), "bundle.zip")
		data, err := ReadBundle(path)
		if err != nil {
			return err
		}
		a := data.Info.Auxiliary
		if a.ControlPort, err = t.port("控制入口端口", data.Info.Node.ControlPort); err != nil {
			return err
		}
		if a.TunnelPort, err = t.port("LAN 到 VPS 的隧道端口", data.Info.Node.TunnelPort); err != nil {
			return err
		}
		rp, err := t.port("Relay TCP/UDP 端口", data.Info.Node.RelayPort)
		if err != nil {
			return err
		}
		stun, err := t.port("STUN UDP 端口", data.Info.Node.STUNPort)
		if err != nil {
			return err
		}
		u, _ := url.Parse(a.Relay)
		a.Relay = "rels://" + net.JoinHostPort(u.Hostname(), strconv.Itoa(rp))
		a.STUN = "stun:" + net.JoinHostPort(u.Hostname(), strconv.Itoa(stun))
		a.Mode = "diagnostic"
		data.Info.Auxiliary = a
		if err = data.rebuild(); err != nil {
			return err
		}
		if err = t.m.ConfigureVPS(data); err != nil {
			return err
		}
		output, err := t.text("供 LAN 同步的新设置文件", "/root/NetGo-vps-settings.json")
		if err != nil {
			return err
		}
		if _, e := os.Stat(output); e == nil {
			return errors.New("文件已存在，请选择新路径")
		}
		if err = t.m.json(output, VPSSettings{Owner: "NetGo VPS settings", SiteID: data.Info.SiteID, Auxiliary: a}); err != nil {
			return err
		}
		t.m.print("新设置已保存：%s。复制到 LAN 后在 ng 菜单选择“设置外网访问 → 配对 / 管理 VPS → 导入 VPS 新设置”，更新 LAN 隧道和客户端入口。", output)
		return nil
	case "repair":
		state, err := t.m.installation()
		if err != nil {
			return err
		}
		if err = t.m.service("start", state.Units...); err != nil {
			return err
		}
		return t.m.DoctorVPS()
	case "uninstall":
		answer, err := t.text("输入 yes 卸载受管服务并保留配置", "")
		if err != nil {
			return err
		}
		if answer == "yes" {
			return t.m.Uninstall()
		}
		return nil
	default:
		return errors.New("无效选项")
	}
}
