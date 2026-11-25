// Package devices owns revocation and retryable upstream peer removal.
package devices

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"netgo/internal/model"
	"netgo/internal/session"
	"netgo/internal/store"
	"sync"
)

type Management interface {
	Request(context.Context, string, string, any, any) (http.Header, error)
}

type Service struct {
	mu         sync.Mutex
	Store      *store.Store
	Sessions   *session.Manager
	Management Management
}

type Result struct {
	Deleted        bool `json:"deleted"`
	CleanupPending bool `json:"cleanup_pending"`
}

// Delete persists revocation before any upstream IO. The tombstone blocks new
// credentials and survives restarts until the remote peer is removed.
func (s *Service) Delete(ctx context.Context, id string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var device model.Device
	err := s.Sessions.MutateDevice(ctx, id, func(st *model.State) error {
		var ok bool
		device, ok = st.Devices[id]
		if !ok {
			return nil
		}
		if !device.DeletePending {
			// Fence already-authorized login transactions for this account without
			// invalidating the active credentials on its other devices.
			u := st.Users[device.UserID]
			if u.AuthRevision == ^uint64(0) {
				return errors.New("authentication revision exhausted")
			}
			u.AuthRevision++
			st.Users[device.UserID] = u
			st.Record("admin", "device.delete.pending", id)
		}
		device.DeletePending = true
		st.Devices[id] = device
		for key, c := range st.Credentials {
			if c.DeviceID == id {
				delete(st.Credentials, key)
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if device.ID == "" {
		return Result{Deleted: true}, nil
	}
	if err = s.remove(ctx, device); err != nil {
		return Result{CleanupPending: true}, nil
	}
	return Result{Deleted: true}, nil
}

func (s *Service) remove(ctx context.Context, device model.Device) error {
	if device.PeerID != "" {
		if s.Management == nil {
			return errors.New("management unavailable")
		}
		_, err := s.Management.Request(ctx, "DELETE", "/peers/"+url.PathEscape(device.PeerID), nil, nil)
		var status interface{ HTTPStatus() int }
		if err != nil && (!errors.As(err, &status) || status.HTTPStatus() != http.StatusNotFound) {
			return err
		}
	}
	return s.Store.Update(func(st *model.State) error {
		if current, ok := st.Devices[device.ID]; ok && current.DeletePending && current.PeerID == device.PeerID {
			delete(st.Devices, device.ID)
			st.Record("system", "device.delete", device.ID)
		}
		return nil
	})
}

// Retry is bounded so an unavailable management service cannot starve sweeps.
func (s *Service) Retry(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.Store.PendingDevices(16)
	if err != nil {
		return err
	}
	for _, d := range items {
		if err := s.remove(ctx, d); err != nil {
			return err
		}
	}
	return nil
}
