package gateway

import (
	"encoding/json"
	"errors"
	"netgo/internal/model"
)

const MaxLeases = 1024
const MaxPolicyBytes = 1 << 20

var ErrCapacity = errors.New("gateway_capacity_exceeded")

// ValidateCapacity is shared by the coordinator and privileged IPC boundary.
// Reject an oversized candidate before touching the currently installed policy.
func ValidateCapacity(leases []model.Lease) error {
	if len(leases) > MaxLeases {
		return ErrCapacity
	}
	b, e := json.Marshal(leases)
	if e != nil {
		return e
	}
	if len(b) > MaxPolicyBytes {
		return ErrCapacity
	}
	return nil
}
