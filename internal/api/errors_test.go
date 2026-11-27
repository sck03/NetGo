package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"netgo/internal/model"
	"netgo/internal/remote"
	"testing"
)

func TestControlAndEditFailuresHaveStableContract(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{model.ErrEditConflict, 409, "edit_conflict"},
		{model.ControlFailure(context.DeadlineExceeded), 503, "control_unavailable"},
		{model.ErrForbidden, 403, "forbidden"},
		{remote.ErrUnavailable, 409, "remote_host_unavailable"},
		{context.Canceled, 408, "control_cancelled"},
	} {
		w := httptest.NewRecorder()
		failure(w, tc.err)
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != tc.status || body["error"] != tc.code {
			t.Fatal(w.Code, w.Body.String(), err)
		}
	}
}
