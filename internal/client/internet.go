package client

import (
	"context"
	"errors"
	"netgo/internal/model"
)

type InternetEngine interface {
	ConfigureInternet(context.Context, bool, []string) error
}

// Do not install an exit route merely because an account has permission.
func SelectedResources(resources []model.Resource, internet bool) ([]model.Resource, error) {
	selected := []model.Resource{}
	granted := false
	for _, r := range resources {
		if !r.Enabled {
			continue
		}
		if r.Protocol == "internet" {
			if r.CIDR != "0.0.0.0/0" || len(r.Ports) != 0 {
				return nil, errors.New("invalid_internet_resource")
			}
			granted = true
			if !internet {
				continue
			}
		}
		selected = append(selected, r)
	}
	if internet && !granted {
		return nil, errors.New("internet_forbidden")
	}
	return selected, nil
}
