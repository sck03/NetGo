//go:build android

package mobile

import (
	"context"
	"errors"
	sdk "github.com/netbirdio/netbird/client/android"
	"netgo/internal/client"
	"netgo/internal/engine/netbird"
	"netgo/internal/model"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Platform supplies OS permissions, TUN ownership and encrypted device storage.
// The VPN service owns it; activities must never own the engine lifecycle.
type Platform interface {
	DeviceStore
	ConfigureInterface(address, address6 string, mtu int, dns, search, routes string) (int, error)
	UpdateAddr(address string) error
	ProtectSocket(fd int32) bool
	IFaces() (string, error)
	NetworkDNS() string
	ConfigureDNS(dns string) error
}
type androidBackend struct {
	mu                                       sync.Mutex
	node                                     *sdk.Client
	platform                                 Platform
	root, device, config, ip, ip6, lastError string
	connected                                bool
	internetDNS                              []string
	done                                     chan struct{}
}
type platformFiles struct{ config, root string }

func (p platformFiles) ConfigurationFilePath() string { return p.config }
func (p platformFiles) StateFilePath() string         { return filepath.Join(p.root, "network-state") }
func (p platformFiles) CacheDir() string              { return filepath.Join(p.root, "network-cache") }
func NewClient(root string, platform Platform, apiLevel int, deviceName string) (*Client, error) {
	if platform == nil || !filepath.IsAbs(root) || apiLevel < 31 {
		return nil, errors.New("invalid_mobile_platform")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	b := &androidBackend{platform: platform, root: root, device: deviceName}
	b.node = sdk.NewClient(apiLevel, deviceName, "NetGo/0.3.0", platform, platform, b)
	b.node.SetConnectionListener(b)
	c := &Client{controller: &client.Controller{Root: filepath.Join(root, "profiles"), Vault: deviceStore{platform}, Engine: b}}
	c.closeNative = func() { b.node.Stop() }
	c.networkChange = func(available bool, _ string) {
		b.node.SetNetworkAvailable(available)
		if available {
			b.node.NotifyNetworkChange()
		}
	}
	return c, nil
}
func (b *androidBackend) auth(p site.Profile) (*sdk.Auth, error) {
	addresses, kinds := []string{}, []string{}
	for _, candidate := range p.Candidates {
		addresses = append(addresses, candidate.Address)
		kinds = append(kinds, candidate.Kind)
	}
	path := filepath.Join(b.root, "netbird-"+p.ID+".json")
	auth, err := sdk.NewNetGoAuth(path, p.Origin, p.CA, strings.Join(addresses, ";"), strings.Join(kinds, ";"), p.GatewayIP, p.LocalRelay)
	if err == nil {
		b.mu.Lock()
		b.config = path
		b.mu.Unlock()
	}
	return auth, err
}
func (b *androidBackend) ConfigureInternet(ctx context.Context, enabled bool, dns []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if enabled && len(dns) == 0 {
		return errors.New("internet_not_configured")
	}
	if err := site.ValidateDNS(dns); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.internetDNS = nil
	if enabled {
		b.internetDNS = append([]string(nil), dns...)
	}
	return b.platform.ConfigureDNS(strings.Join(b.internetDNS, ";"))
}
func (b *androidBackend) Prepare(ctx context.Context, p site.Profile) (string, error) {
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
func (b *androidBackend) Login(ctx context.Context, p site.Profile, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	auth, err := b.auth(p)
	if err != nil {
		return err
	}
	if err = auth.NetGoLoginJWT(token, b.device); err != nil {
		return err
	}
	return ctx.Err()
}
func (b *androidBackend) dns() *sdk.DNSList {
	list := &sdk.DNSList{}
	for _, ip := range strings.Split(b.platform.NetworkDNS(), ";") {
		if ip != "" {
			_ = list.Add(ip)
		}
	}
	return list
}
func (b *androidBackend) Up(ctx context.Context, _ site.Profile) error {
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
	done := make(chan struct{})
	b.done = done
	files := platformFiles{b.config, b.root}
	env := sdk.NewEnvList()
	env.Put("NB_LAZY_CONN", "false")
	go func() {
		err := b.node.RunWithoutLogin(files, b.dns(), b, env)
		b.mu.Lock()
		b.connected = false
		if err != nil {
			b.lastError = "network_engine_failed"
		}
		if b.done == done {
			b.done = nil
		}
		b.mu.Unlock()
		close(done)
	}()
	return nil
}
func (b *androidBackend) Down(ctx context.Context) error {
	b.node.Stop()
	b.mu.Lock()
	done := b.done
	b.connected = false
	b.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return errors.New("network_cleanup_timeout")
		}
	}
	return nil
}
func (b *androidBackend) Status(ctx context.Context, p site.Profile) (netbird.DataStatus, error) {
	if err := ctx.Err(); err != nil {
		return netbird.DataStatus{}, err
	}
	b.mu.Lock()
	out := netbird.DataStatus{State: "disconnected", Path: "negotiating", IP: strings.Split(b.ip, "/")[0]}
	if b.connected {
		out.State = "connected"
	}
	b.mu.Unlock()
	peers := b.node.PeersList()
	for i := 0; i < peers.Size(); i++ {
		peer := peers.Get(i)
		out.RX += peer.BytesRx
		out.TX += peer.BytesTx
		if strings.Split(peer.IP, "/")[0] == p.GatewayIP && peer.ConnStatus == sdk.ConnStatusConnected {
			if peer.Relayed {
				out.Path = "relay"
			} else {
				out.Path = "direct"
			}
		}
	}
	return out, nil
}
func (b *androidBackend) WaitConnected(ctx context.Context, p site.Profile) error {
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
func (b *androidBackend) Routes(ctx context.Context, resources []model.Resource) error {
	wanted := map[string]bool{}
	for _, r := range resources {
		wanted[r.CIDR] = true
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		routes := b.node.Networks()
		found := map[string]bool{}
		for i := 0; i < routes.Size(); i++ {
			network := routes.Get(i)
			if wanted[network.Network] {
				found[network.Network] = true
			}
		}
		if len(found) == len(wanted) {
			for i := 0; i < routes.Size(); i++ {
				network := routes.Get(i)
				var err error
				if wanted[network.Network] {
					err = b.node.SelectRoute(network.Name)
				} else {
					err = b.node.DeselectRoute(network.Name)
				}
				if err != nil {
					return err
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func (b *androidBackend) OnStateChanged(_ int) {}
func (b *androidBackend) OnConnected()         { b.mu.Lock(); b.connected = true; b.mu.Unlock() }
func (b *androidBackend) OnDisconnected()      { b.mu.Lock(); b.connected = false; b.mu.Unlock() }
func (b *androidBackend) OnConnecting()        { b.OnDisconnected() }
func (b *androidBackend) OnDisconnecting()     { b.OnDisconnected() }
func (b *androidBackend) OnAddressChanged(ip, ip6 string) {
	b.mu.Lock()
	b.ip = ip
	b.ip6 = ip6
	b.mu.Unlock()
}
func (b *androidBackend) OnPeersListChanged(_ int)   {}
func (b *androidBackend) SetInterfaceIP(ip string)   { b.mu.Lock(); b.ip = ip; b.mu.Unlock() }
func (b *androidBackend) SetInterfaceIPv6(ip string) { b.mu.Lock(); b.ip6 = ip; b.mu.Unlock() }
func (b *androidBackend) OnNetworkChanged(_ string)  { b.rebuildTun() }
func (b *androidBackend) OnReady()                   { b.rebuildTun() }
func (b *androidBackend) rebuildTun() {
	settings, err := b.node.GetTunSettings()
	if err != nil {
		return
	}
	b.mu.Lock()
	ip, ip6 := b.ip, b.ip6
	b.mu.Unlock()
	if ip == "" {
		return
	}
	b.mu.Lock()
	dns := strings.Join(b.internetDNS, ";")
	b.mu.Unlock()
	fd, err := b.platform.ConfigureInterface(ip, ip6, 1280, dns, settings.SearchDomains, settings.Routes)
	if err != nil {
		return
	}
	_ = b.node.RenewTun(fd)
}
