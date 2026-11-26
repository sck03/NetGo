package client

import (
	"context"
	"netgo/internal/model"
	"testing"
)

func TestRemoteReadinessIsIndependentOfGatewayPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Controller{remote: &remoteConnection{ctx: ctx, site: "site"}, status: Status{SiteID: "site", State: "negotiating", Lease: model.Lease{ID: "lease"}}}
	if !c.Status().RemoteReady {
		t.Fatal("authorized remote path gated on LAN gateway")
	}
	c.status.State = "recovering"
	if c.Status().RemoteReady {
		t.Fatal("recovery advertised ready")
	}
	c.status.State = "connected"
	cancel()
	if c.Status().RemoteReady {
		t.Fatal("cancelled connection advertised ready")
	}
}
