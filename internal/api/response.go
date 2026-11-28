package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"netgo/internal/gateway"
	"netgo/internal/model"
	"netgo/internal/remote"
	"netgo/internal/session"
	"netgo/internal/site"
)

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, e error) {
	status := 400
	code := "invalid_request"
	switch {
	case errors.Is(e, model.ErrEditConflict):
		status, code = http.StatusConflict, "edit_conflict"
	case errors.Is(e, model.ErrControlUnavailable), errors.Is(e, context.DeadlineExceeded):
		status, code = http.StatusServiceUnavailable, "control_unavailable"
	case errors.Is(e, model.ErrControlCapacity):
		status, code = http.StatusServiceUnavailable, "control_capacity_exceeded"
		w.Header().Set("Retry-After", "1")
	case errors.Is(e, context.Canceled):
		status, code = http.StatusRequestTimeout, "control_cancelled"
	case errors.Is(e, session.ErrStopped):
		status = http.StatusServiceUnavailable
		code = "session_manager_stopped"
	case errors.Is(e, model.ErrUnauthorized):
		status = 401
		code = "unauthorized"
	case errors.Is(e, model.ErrForbidden):
		status = 403
		code = "forbidden"
	case errors.Is(e, remote.ErrBusy):
		status = 409
		code = "remote_host_busy"
	case errors.Is(e, remote.ErrUnavailable):
		status = 409
		code = "remote_host_unavailable"
	case errors.Is(e, gateway.ErrCapacity):
		status = 503
		code = "gateway_capacity_exceeded"
	case errors.Is(e, model.ErrConflict):
		status = 409
		code = "session_conflict"
	case errors.Is(e, model.ErrNotFound):
		status = 404
		code = "not_found"
	case e.Error() == "rate_limited":
		status = http.StatusTooManyRequests
		code = "rate_limited"
	default:
		code = e.Error()
		if len(code) > 160 {
			code = "operation_failed"
		}
	}
	reply(w, status, map[string]string{"error": code})
}
func body(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	return site.Decode(r.Body, v)
}
