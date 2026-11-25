package node

import (
	"errors"
	"io"
)

var errMenuBack = errors.New("menu back")

type menuItem struct {
	key, label string
	run        func() error
}

// Each screen owns its actions; navigation does not know deployment details.
func (t terminal) menu(title string, items ...menuItem) error {
	for {
		t.m.print("\n%s", title)
		for _, item := range items {
			t.m.print("%s) %s", item.key, item.label)
		}
		t.m.print("0) 返回 / 退出")
		choice, err := t.text("请选择", "0")
		if errors.Is(err, io.EOF) || choice == "0" {
			return nil
		}
		if err != nil {
			return err
		}
		found := false
		for _, item := range items {
			if item.key != choice {
				continue
			}
			found = true
			if err = item.run(); errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil && !errors.Is(err, errMenuBack) {
				t.m.print("操作未完成：%s", err)
			}
			break
		}
		if !found {
			t.m.print("请输入菜单中的编号。")
		}
	}
}

func (t terminal) serverAction(action string) func() error {
	return func() error { return t.serverOperation(action) }
}

func (t terminal) serverMenu() error {
	return t.menu("NetGo 局域网服务端 · ng",
		menuItem{"1", "首次配置 / 启动服务", t.serverAction("setup")},
		menuItem{"2", "查看连接与运行状态", t.serverAction("status")},
		menuItem{"3", "设置外网访问", func() error {
			t.m.print("没有公网入口时可配对 VPS；有公网 IP/域名时可配置直达入口。程序不会自动配置路由器端口映射。")
			return t.menu("外网访问",
				menuItem{"1", "配对 / 管理 VPS", t.serverAction("auxiliary")},
				menuItem{"2", "管理 LAN / 公网直达入口", t.serverAction("endpoints")},
				menuItem{"3", "查看当前连接方式", t.serverAction("status")})
		}},
		menuItem{"4", "添加客户端（导出站点文件）", t.serverAction("export")},
		menuItem{"5", "高级网络与证书设置", func() error {
			return t.menu("高级网络与证书设置",
				menuItem{"1", "修改站点身份与服务端口", t.serverAction("identity")},
				menuItem{"2", "生成 / 导入证书", t.serverAction("certificates")},
				menuItem{"3", "设置互联网出口（旁路由 / DNS）", t.serverAction("bypass")})
		}},
		menuItem{"6", "检查并恢复服务", t.serverAction("repair")},
		menuItem{"9", "卸载（保留数据）", t.serverAction("uninstall")})
}
