//go:build ios

package mobile

import (
	"context"
	"errors"
	sdk "github.com/netbirdio/netbird/client/ios/NetBirdSDK"
	"netgo/internal/client"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Platform interface {
	DeviceStore
	TunnelFD() int
	InterfaceName() string
	ConfigureNetwork(address, address6, dns, routes string) error
}
type iosBackend struct {
	mu                                 sync.Mutex
	node                               *sdk.Client
	platform                           Platform
	root, device, config, ip, ip6, dns string
	connected                          bool
	done                               chan struct{}
}

func NewClient(root string, platform Platform, apiLevel int, deviceName string) (*Client, error) {
	if platform == nil || !filepath.IsAbs(root) || apiLevel < 17 {
		return nil, errors.New("invalid_apple_platform")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	b := &iosBackend{root: root, platform: platform, device: deviceName}
	c := &Client{controller: &client.Controller{Root: filepath.Join(root, "profiles"), Vault: deviceStore{platform}, Engine: b}}
	c.closeNative = func() {
		b.mu.Lock()
		node := b.node
		b.mu.Unlock()
		if node != nil {
			node.Stop()
		}
	}
	c.networkChange = func(available bool, _ string) {
		b.mu.Lock()
		node := b.node
		b.mu.Unlock()
		if node != nil {
			node.SetNetworkAvailable(available)
			if available {
				node.NotifyNetworkChange()
			}
		}
	}
	return c, nil
}
func (b *iosBackend) auth(p site.Profile) (*sdk.Auth, error) {
	addresses, kinds := []string{}, []string{}
	for _, candidate := range p.Candidates {
		addresses = append(addresses, candidate.Address)
		kinds = append(kinds, candidate.Kind)
	}
	b.config = filepath.Join(b.root, "netbird-"+p.ID+".json")
	return sdk.NewNetGoAuth(b.config, p.Origin, p.CA, strings.Join(addresses, ";"), strings.Join(kinds, ";"))
}
func (b *iosBackend) Prepare(ctx context.Context, p site.Profile) (string, error) {
	if err := b.Down(ctx); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	auth, err := b.auth(p)
	if err != nil {
		return "", err
	}
	return auth.NetGoPublicKey()
}
func (b *iosBackend) Login(ctx context.Context, p site.Profile, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	auth, err := b.auth(p)
	if err != nil {
		return err
	}
	return auth.NetGoLoginJWT(token, b.device)
}
func (b *iosBackend) Up(ctx context.Context, _ site.Profile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done != nil {
		return nil
	}
	if b.config == "" {
		return errors.New("network_not_prepared")
	}
	fd, err := syscall.Dup(b.platform.TunnelFD())
	if err != nil {
		return err
	}
	b.node = sdk.NewClient(b.config, filepath.Join(b.root, "network-state"), filepath.Join(b.root, "network-cache"), "console", b.device, "17", "iOS", b, b)
	b.node.SetConnectionListener(b)
	node := b.node
	done := make(chan struct{})
	b.done = done
	env := sdk.NewEnvList()
	env.Put("NB_LAZY_CONN", "false")
	go func() {
		_ = node.Run(int32(fd), b.platform.InterfaceName(), env)
		b.mu.Lock()
		b.connected = false
		if b.done == done {
			b.done = nil
		}
		b.mu.Unlock()
		close(done)
	}()
	return nil
}
func (b *iosBackend) Down(ctx context.Context) error {
	b.mu.Lock()
	node, done := b.node, b.done
	b.connected = false
	b.mu.Unlock()
	if node != nil {
		node.StopWithoutWait()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return errors.New("network_cleanup_timeout")
		}
	}
	return nil
}
func (b *iosBackend) Status(ctx context.Context, p site.Profile) (netbird.DataStatus, error) {
	if err := ctx.Err(); err != nil {
		return netbird.DataStatus{}, err
	}
	b.mu.Lock()
	node, connected, ip := b.node, b.connected, b.ip
	b.mu.Unlock()
	out := netbird.DataStatus{State: "disconnected", Path: "negotiating", IP: strings.Split(ip, "/")[0]}
	if connected {
		out.State = "connected"
	}
	if node == nil {
		return out, nil
	}
	details := node.GetStatusDetails()
	for n := 0; n < details.Size(); n++ {
		peer := details.Get(n)
		out.RX += peer.BytesRx
		out.TX += peer.BytesTx
		if strings.Split(peer.IP, "/")[0] == p.GatewayIP && strings.EqualFold(peer.ConnStatus, "connected") {
			out.Path = "direct"
			if peer.Relayed {
				out.Path = "relay"
			}
		}
	}
	return out, nil
}
func (b *iosBackend) WaitConnected(ctx context.Context, p site.Profile) error {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		state, err := b.Status(ctx, p)
		if err != nil {
			return err
		}
		if state.State == "connected" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func (b *iosBackend) Routes(ctx context.Context, resources []model.Resource) error {
	wanted := map[string]bool{}
	for _, r := range resources {
		wanted[r.CIDR] = true
	}
	b.mu.Lock()
	node := b.node
	b.mu.Unlock()
	if node == nil {
		return errors.New("network_not_ready")
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		routes, err := node.GetRoutesSelectionDetails()
		if err != nil {
			return err
		}
		found := map[string]bool{}
		for n := 0; n < routes.Size(); n++ {
			r := routes.Get(n)
			if wanted[r.Network] {
				found[r.Network] = true
			}
		}
		if len(found) == len(wanted) {
			for n := 0; n < routes.Size(); n++ {
				r := routes.Get(n)
				if wanted[r.Network] {
					err = node.SelectRoute(r.ID)
				} else {
					err = node.DeselectRoute(r.ID)
				}
				if err != nil {
					return err
				}
			}
			b.configure()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func (b *iosBackend) configure() {
	b.mu.Lock()
	node, ip, ip6, dns := b.node, b.ip, b.ip6, b.dns
	b.mu.Unlock()
	if node == nil || ip == "" {
		return
	}
	routes, err := node.GetRoutesSelectionDetails()
	if err != nil {
		return
	}
	selected := []string{}
	for n := 0; n < routes.Size(); n++ {
		r := routes.Get(n)
		if r.Selected {
			selected = append(selected, r.Network)
		}
	}
	_ = b.platform.ConfigureNetwork(ip, ip6, dns, strings.Join(selected, ";"))
}
func (b *iosBackend) ApplyDns(value string) { b.mu.Lock(); b.dns = value; b.mu.Unlock(); b.configure() }
func (b *iosBackend) SetInterfaceIP(value string) {
	b.mu.Lock()
	b.ip = value
	b.mu.Unlock()
	b.configure()
}
func (b *iosBackend) SetInterfaceIPv6(value string) {
	b.mu.Lock()
	b.ip6 = value
	b.mu.Unlock()
	b.configure()
}
func (b *iosBackend) OnNetworkChanged(string) { b.configure() }
func (b *iosBackend) OnStateChanged(int)      {}
func (b *iosBackend) OnConnected()            { b.mu.Lock(); b.connected = true; b.mu.Unlock(); b.configure() }
func (b *iosBackend) OnDisconnected()         { b.mu.Lock(); b.connected = false; b.mu.Unlock() }
func (b *iosBackend) OnConnecting()           { b.OnDisconnected() }
func (b *iosBackend) OnDisconnecting()        { b.OnDisconnected() }
func (b *iosBackend) OnAddressChanged(ip, ip6 string) {
	b.mu.Lock()
	b.ip = ip
	b.ip6 = ip6
	b.mu.Unlock()
	b.configure()
}
func (b *iosBackend) OnPeersListChanged(int) {}
