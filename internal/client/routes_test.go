package client

import (
	"context"
	"errors"
	"netgo/internal/model"
	"testing"
	"time"
)

type routeProbe func(context.Context, []model.Resource) error

func (p routeProbe) Routes(ctx context.Context, r []model.Resource) error { return p(ctx, r) }
func TestRouteOperationBoundsLifetimeContext(t *testing.T) {
	var operation context.Context
	probe := routeProbe(func(ctx context.Context, _ []model.Resource) error {
		operation = ctx
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > routeTimeout || time.Until(deadline) <= 0 {
			t.Fatal("route operation has no bounded deadline")
		}
		return nil
	})
	parent := context.Background()
	if err := applySelectedRoutes(parent, probe, nil, false); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(operation.Err(), context.Canceled) || parent.Err() != nil {
		t.Fatal("operation cleanup affected connection lifetime")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Millisecond)
	defer cancel()
	err := applySelectedRoutes(ctx, routeProbe(func(ctx context.Context, _ []model.Resource) error { <-ctx.Done(); return ctx.Err() }), nil, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing route did not honor shorter recovery deadline: %v", err)
	}
}
