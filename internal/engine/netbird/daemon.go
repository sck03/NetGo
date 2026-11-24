package netbird

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/netbirdio/netbird/client/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"net"
	"netgo/internal/ipc"
	"netgo/internal/model"
	"netgo/internal/site"
	"os/user"
	"strings"
	"time"
)

type Daemon struct {
	RPC      pb.DaemonServiceClient
	conn     *grpc.ClientConn
	Username string
	internet bool
}

func (d *Daemon) ConfigureInternet(ctx context.Context, enabled bool, dns []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if enabled && len(dns) == 0 {
		return errors.New("internet_not_configured")
	}
	if err := site.ValidateDNS(dns); err != nil {
		return err
	}
	d.internet = enabled
	return nil
}

func OpenDaemon(path string) (*Daemon, error) {
	u, e := user.Current()
	if e != nil {
		return nil, e
	}
	conn, e := grpc.NewClient("passthrough:///netbird", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ipc.Dial(ctx, path) }))
	if e != nil {
		return nil, e
	}
	return &Daemon{RPC: pb.NewDaemonServiceClient(conn), conn: conn, Username: u.Username}, nil
}
func (d *Daemon) Close() error { return d.conn.Close() }
func boolp(v bool) *bool       { return &v }
func (d *Daemon) login(ctx context.Context, p site.Profile, mode, token string) (*pb.LoginResponse, error) {
	name := "netgo-" + p.ID
	md := metadata.Pairs("netgo-mode", mode)
	for _, c := range p.Candidates {
		md.Append("netgo-candidate", c.Address)
		md.Append("netgo-candidate-kind", c.Kind)
	}
	if token != "" {
		md.Append("netgo-jwt", token)
	}
	if p.CA != "" {
		md.Append("netgo-ca-bin", p.CA)
	}
	ctx = metadata.NewOutgoingContext(ctx, md)
	return d.RPC.Login(ctx, &pb.LoginRequest{ManagementUrl: p.Origin, ProfileName: &name, Username: &d.Username, DisableDns: boolp(!d.internet), DisableAutoConnect: boolp(true), ServerSSHAllowed: boolp(false), DisableServerRoutes: boolp(true), DisableIpv6: boolp(true), BlockInbound: boolp(true)})
}
func (d *Daemon) Prepare(ctx context.Context, p site.Profile) (string, error) {
	if _, e := d.RPC.Down(ctx, &pb.DownRequest{}); e != nil {
		return "", e
	}
	name := "netgo-" + p.ID
	profiles, e := d.RPC.ListProfiles(ctx, &pb.ListProfilesRequest{Username: d.Username})
	if e != nil {
		return "", e
	}
	found := false
	for _, profile := range profiles.GetProfiles() {
		if profile.GetName() == name {
			if found {
				return "", errors.New("ambiguous NetGo profile")
			}
			found = true
		}
	}
	// AddProfile permits duplicate display names in the pinned daemon. Reuse
	// the current user's existing profile so reconnecting preserves its key.
	if !found {
		if _, e = d.RPC.AddProfile(ctx, &pb.AddProfileRequest{Username: d.Username, ProfileName: name}); e != nil {
			return "", e
		}
	}
	r, e := d.login(ctx, p, "prepare", "")
	if e != nil {
		return "", e
	}
	if r.UserCode == "" || r.NeedsSSOLogin {
		return "", errors.New("patched NetBird native daemon required")
	}
	return r.UserCode, nil
}
func (d *Daemon) Login(ctx context.Context, p site.Profile, token string) error {
	_, e := d.login(ctx, p, "native", token)
	return e
}
func (d *Daemon) Up(ctx context.Context, p site.Profile) error {
	name := "netgo-" + p.ID
	_, e := d.RPC.Up(ctx, &pb.UpRequest{ProfileName: &name, Username: &d.Username, Async: true})
	return e
}
func (d *Daemon) Down(ctx context.Context) error {
	_, e := d.RPC.Down(ctx, &pb.DownRequest{})
	return e
}

type DataStatus struct {
	State string `json:"state"`
	Path  string `json:"path"`
	IP    string `json:"ip"`
	Relay string `json:"relay"`
	RX    int64  `json:"rx"`
	TX    int64  `json:"tx"`
}

func (d *Daemon) Status(ctx context.Context, p site.Profile) (DataStatus, error) {
	v, e := d.RPC.Status(ctx, &pb.StatusRequest{GetFullPeerStatus: true})
	if e != nil {
		return DataStatus{}, e
	}
	out := DataStatus{State: v.Status, Path: "negotiating"}
	if f := v.FullStatus; f != nil {
		if l := f.LocalPeerState; l != nil {
			out.IP = l.IP
		}
		for _, peer := range f.Peers {
			if strings.Split(peer.IP, "/")[0] != p.GatewayIP {
				continue
			}
			out.RX = peer.BytesRx
			out.TX = peer.BytesTx
			if !strings.EqualFold(peer.ConnStatus, "connected") {
				continue
			}
			out.Path = "direct"
			if peer.Relayed {
				out.Relay = peer.RelayAddress
				out.Path = "relay_unknown"
				if relayMatches(peer.RelayAddress, p.LocalRelay) {
					out.Path = "local_relay"
				} else if relayMatches(peer.RelayAddress, p.AuxiliaryRelay) {
					out.Path = "auxiliary_relay"
				}
			}
			break
		}
	}
	return out, nil
}
func (d *Daemon) Routes(ctx context.Context, resources []model.Resource) error {
	if len(resources) == 0 {
		_, err := d.RPC.SelectNetworks(ctx, &pb.SelectNetworksRequest{NetworkIDs: []string{}, Append: false})
		return err
	}
	// Management policy writes precede asynchronous route delivery to the daemon.
	// Match the Windows adapter's bounded wait without selecting a partial map.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("authorized route unavailable: %w", err)
		}
		v, err := d.RPC.ListNetworks(ctx, &pb.ListNetworksRequest{})
		if err != nil {
			return err
		}
		ids := []string{}
		seen := map[string]bool{}
		complete := true
		for _, r := range resources {
			found := false
			for _, n := range v.GetRoutes() {
				if n.GetRange() == r.CIDR && n.GetID() != "" {
					if !seen[n.ID] {
						ids = append(ids, n.ID)
						seen[n.ID] = true
					}
					found = true
				}
			}
			complete = complete && found
		}
		if complete {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err = d.RPC.SelectNetworks(ctx, &pb.SelectNetworksRequest{NetworkIDs: ids, Append: false})
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("authorized route unavailable: %w", ctx.Err())
		case <-tick.C:
		}
	}
}
func (d *Daemon) WaitConnected(ctx context.Context, p site.Profile) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		v, e := d.Status(ctx, p)
		if e == nil && strings.EqualFold(v.State, "connected") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
