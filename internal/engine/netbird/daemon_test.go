package netbird

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	pb "github.com/netbirdio/netbird/client/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"netgo/internal/model"
	"netgo/internal/site"
)

type daemonRPC struct {
	pb.DaemonServiceClient
	profiles      []*pb.Profile
	added         int
	logins        int
	listNetworks  func(context.Context) (*pb.ListNetworksResponse, error)
	selected      *pb.SelectNetworksRequest
	selectErr     error
	loginMetadata metadata.MD
	loginRequest  *pb.LoginRequest
}

func (r *daemonRPC) Down(context.Context, *pb.DownRequest, ...grpc.CallOption) (*pb.DownResponse, error) {
	return &pb.DownResponse{}, nil
}

func (r *daemonRPC) ListProfiles(context.Context, *pb.ListProfilesRequest, ...grpc.CallOption) (*pb.ListProfilesResponse, error) {
	return &pb.ListProfilesResponse{Profiles: r.profiles}, nil
}

func (r *daemonRPC) AddProfile(_ context.Context, req *pb.AddProfileRequest, _ ...grpc.CallOption) (*pb.AddProfileResponse, error) {
	// The pinned daemon permits duplicate display names; it does not return AlreadyExists.
	r.profiles = append(r.profiles, &pb.Profile{Name: req.ProfileName})
	r.added++
	return &pb.AddProfileResponse{}, nil
}

func (r *daemonRPC) Login(ctx context.Context, req *pb.LoginRequest, _ ...grpc.CallOption) (*pb.LoginResponse, error) {
	r.loginRequest = req
	r.logins++
	r.loginMetadata, _ = metadata.FromOutgoingContext(ctx)
	return &pb.LoginResponse{UserCode: "same-device-key"}, nil
}

func TestLoginPassesCandidateKindsWithAddresses(t *testing.T) {
	rpc := &daemonRPC{}
	d := &Daemon{RPC: rpc, Username: "user"}
	profile := site.Profile{ID: "site", Candidates: []site.Candidate{
		{ID: "backup", Address: "backup.example:24443", Kind: "auxiliary"},
		{ID: "wan2", Address: "wan2.example:10443", Kind: "direct"},
	}}
	if err := d.Login(context.Background(), profile, "test-token"); err != nil {
		t.Fatal(err)
	}
	if rpc.loginRequest.BlockInbound == nil || *rpc.loginRequest.BlockInbound {
		t.Fatal("managed inbound ACL is bypassed")
	}
	if !reflect.DeepEqual(rpc.loginMetadata.Get("netgo-candidate-kind"), []string{"auxiliary", "direct"}) ||
		!reflect.DeepEqual(rpc.loginMetadata.Get("netgo-candidate"), []string{"backup.example:24443", "wan2.example:10443"}) {
		t.Fatal("core candidate preferences were lost or reordered")
	}
}

func (r *daemonRPC) ListNetworks(ctx context.Context, _ *pb.ListNetworksRequest, _ ...grpc.CallOption) (*pb.ListNetworksResponse, error) {
	return r.listNetworks(ctx)
}

func (r *daemonRPC) SelectNetworks(_ context.Context, req *pb.SelectNetworksRequest, _ ...grpc.CallOption) (*pb.SelectNetworksResponse, error) {
	r.selected = req
	return &pb.SelectNetworksResponse{}, r.selectErr
}

func TestPrepareReusesProfileAcrossReconnects(t *testing.T) {
	rpc := &daemonRPC{profiles: []*pb.Profile{{Name: "unrelated"}}}
	d := &Daemon{RPC: rpc, Username: "user"}
	for range 2 {
		key, err := d.Prepare(context.Background(), site.Profile{ID: "site"})
		if err != nil || key != "same-device-key" {
			t.Fatalf("prepare failed: key=%q error=%v", key, err)
		}
	}
	if rpc.added != 1 || len(rpc.profiles) != 2 {
		t.Fatalf("reconnect created duplicate profiles: additions=%d profiles=%d", rpc.added, len(rpc.profiles))
	}
}

func TestPrepareRejectsAmbiguousProfile(t *testing.T) {
	rpc := &daemonRPC{profiles: []*pb.Profile{{Name: "netgo-site"}, {Name: "netgo-site"}}}
	d := &Daemon{RPC: rpc, Username: "user"}
	if _, err := d.Prepare(context.Background(), site.Profile{ID: "site"}); err == nil {
		t.Fatal("ambiguous profile was accepted")
	}
	if rpc.added != 0 || rpc.logins != 0 {
		t.Fatal("ambiguous profile changed daemon identity")
	}
}

func TestRoutesWaitsForAuthorizedNetworkMap(t *testing.T) {
	calls := 0
	rpc := &daemonRPC{listNetworks: func(context.Context) (*pb.ListNetworksResponse, error) {
		calls++
		routes := []*pb.Network{{ID: "unrelated", Range: "10.20.0.0/16"}}
		if calls >= 2 {
			routes = append(routes, &pb.Network{ID: "nas", Range: "192.168.50.20/32"})
		}
		return &pb.ListNetworksResponse{Routes: routes}, nil
	}}
	d := &Daemon{RPC: rpc}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resources := []model.Resource{{ID: "nas-https", CIDR: "192.168.50.20/32"}, {ID: "nas-ssh", CIDR: "192.168.50.20/32"}}
	if err := d.Routes(ctx, resources); err != nil {
		t.Fatalf("delayed authorized route was rejected: %v", err)
	}
	if rpc.selected == nil || rpc.selected.Append || !reflect.DeepEqual(rpc.selected.NetworkIDs, []string{"nas"}) {
		t.Fatalf("unexpected selected routes: %v", rpc.selected)
	}
}

func TestRoutesCancelsWithoutSelectingPartialMap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rpc := &daemonRPC{listNetworks: func(context.Context) (*pb.ListNetworksResponse, error) {
		cancel()
		return &pb.ListNetworksResponse{Routes: []*pb.Network{{ID: "nas", Range: "192.168.50.20/32"}}}, nil
	}}
	d := &Daemon{RPC: rpc}
	err := d.Routes(ctx, []model.Resource{{CIDR: "192.168.50.20/32"}, {CIDR: "192.168.50.30/32"}})
	if !errors.Is(err, context.Canceled) || rpc.selected != nil {
		t.Fatalf("cancellation selected an incomplete route set: selected=%v error=%v", rpc.selected, err)
	}
}

func TestRoutesPropagatesDaemonFailures(t *testing.T) {
	want := errors.New("daemon unavailable")
	for _, scenario := range []string{"list", "select"} {
		t.Run(scenario, func(t *testing.T) {
			rpc := &daemonRPC{listNetworks: func(context.Context) (*pb.ListNetworksResponse, error) {
				if scenario == "list" {
					return nil, want
				}
				return &pb.ListNetworksResponse{Routes: []*pb.Network{{ID: "nas", Range: "192.168.50.20/32"}}}, nil
			}, selectErr: want}
			d := &Daemon{RPC: rpc}
			if err := d.Routes(context.Background(), []model.Resource{{CIDR: "192.168.50.20/32"}}); !errors.Is(err, want) {
				t.Fatalf("daemon failure lost: %v", err)
			}
		})
	}
}
