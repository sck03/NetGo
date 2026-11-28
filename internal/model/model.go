package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

const Protocol = 2
const Schema = 8

// Version is set by the build scripts through the Go linker; protocol/schema
// remain fixed independently of the selected release version.
var Version = "0.3.0-dev"

var (
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrConflict           = errors.New("session_conflict")
	ErrNotFound           = errors.New("not_found")
	ErrEditConflict       = errors.New("edit_conflict")
	ErrControlUnavailable = errors.New("control_unavailable")
	ErrControlCapacity    = errors.New("control_capacity_exceeded")
)

func ID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Challenge(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type User struct {
	CleanupRetry
	DeletePending bool          `json:"delete_pending"`
	ID            string        `json:"id"`
	Alias         string        `json:"alias"`
	Name          string        `json:"name"`
	Email         string        `json:"email"`
	Role          string        `json:"role"`
	Disabled      bool          `json:"disabled"`
	ExpiresAt     time.Time     `json:"expires_at"`
	MaxDevices    int           `json:"max_devices"`
	MaxSessions   int           `json:"max_sessions"`
	Resources     Array[string] `json:"resources"`
	AuthRevision  uint64        `json:"auth_revision,omitempty"`
	EditRevision  uint64        `json:"edit_revision"`
}

func (u User) Active(now time.Time) bool {
	return !u.Disabled && !u.DeletePending && (u.ExpiresAt.IsZero() || now.Before(u.ExpiresAt))
}

type Resource struct {
	EditToken string        `json:"edit_token"`
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	CIDR      string        `json:"cidr"`
	Protocol  string        `json:"protocol"`
	Ports     Array[uint16] `json:"ports"`
	Enabled   bool          `json:"enabled"`
}
type Device struct {
	CleanupRetry
	DeletePending bool      `json:"delete_pending"`
	ClientType    string    `json:"client_type,omitempty"`
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	PublicKey     string    `json:"public_key"`
	WireGuardKey  string    `json:"wireguard_key"`
	PeerID        string    `json:"peer_id"`
	IP            string    `json:"ip"`
	CreatedAt     time.Time `json:"created_at"`
}
type Lease struct {
	ID         string          `json:"id"`
	UserID     string          `json:"user_id"`
	DeviceID   string          `json:"device_id"`
	PeerID     string          `json:"peer_id"`
	IP         string          `json:"ip"`
	Generation uint64          `json:"generation"`
	ExpiresAt  time.Time       `json:"expires_at"`
	Resources  Array[Resource] `json:"resources"`
}
type Credential struct {
	// TokenHash is attached by request authentication, never persisted or exported.
	TokenHash string           `json:"-"`
	UserID    string           `json:"user_id"`
	DeviceID  string           `json:"device_id"`
	ExpiresAt time.Time        `json:"expires_at"`
	Recovery  *RefreshRecovery `json:"refresh,omitempty"`
}

// Recovery is owned by the replacement credential and revoked with it.
// Response is encrypted with the original device-bound refresh request.
type RefreshRecovery struct {
	PreviousHash string    `json:"previous_hash"`
	Until        time.Time `json:"until"`
	Response     []byte    `json:"response"`
}
type Event struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Object string    `json:"object"`
}
type Operation struct {
	Fingerprint string    `json:"fingerprint"`
	LeaseID     string    `json:"lease_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type State struct {
	Schema           int                        `json:"schema"`
	Revision         uint64                     `json:"revision"`
	ObservedRevision uint64                     `json:"observed_revision"`
	LastError        string                     `json:"last_error,omitempty"`
	Users            map[string]User            `json:"users"`
	Resources        map[string]Resource        `json:"resources"`
	Devices          map[string]Device          `json:"devices"`
	Leases           map[string]Lease           `json:"leases"`
	Credentials      map[string]Credential      `json:"credentials"`
	Operations       map[string]Operation       `json:"operations"`
	Audit            []Event                    `json:"audit"`
	Hosts            map[string]Host            `json:"hosts"`
	RemoteGrants     map[string]RemoteGrant     `json:"remote_grants"`
	RemoteSessions   map[string]RemoteSession   `json:"remote_sessions"`
	RemoteOperations map[string]RemoteOperation `json:"remote_operations"`
}

func NewState() State {
	return State{Schema: Schema, Users: map[string]User{}, Resources: map[string]Resource{}, Devices: map[string]Device{}, Leases: map[string]Lease{}, Credentials: map[string]Credential{}, Operations: map[string]Operation{}, Audit: []Event{}, Hosts: map[string]Host{}, RemoteGrants: map[string]RemoteGrant{}, RemoteSessions: map[string]RemoteSession{}, RemoteOperations: map[string]RemoteOperation{}}
}
func (s *State) Record(actor, action, object string) {
	s.Audit = append(s.Audit, Event{time.Now().UTC(), actor, action, object})
	if len(s.Audit) > 10000 {
		s.Audit = s.Audit[len(s.Audit)-10000:]
	}
}

// InvalidateUser fences authentication already in progress as well as issued tokens.
// The revision belongs to NetGo metadata; password material remains in the IdP.
func (s *State) InvalidateUser(id string) error {
	u, ok := s.Users[id]
	if !ok {
		return ErrNotFound
	}
	if u.AuthRevision == ^uint64(0) {
		return errors.New("authentication revision exhausted")
	}
	u.AuthRevision++
	s.Users[id] = u
	for key, c := range s.Credentials {
		if c.UserID == id {
			delete(s.Credentials, key)
		}
	}
	return nil
}
