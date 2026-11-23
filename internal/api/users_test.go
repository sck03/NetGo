package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"netgo/internal/engine/netbird"
	"netgo/internal/store"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentUserCreationPreservesUniqueAlias(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/users" {
			t.Errorf("unexpected management request: %s %s", r.Method, r.URL.Path)
		}
		id := calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprint(id), "password": "initial-password"})
	}))
	defer upstream.Close()
	s := &Server{Store: db, Management: &netbird.Management{URL: upstream.URL, Client: upstream.Client()}}
	var wg sync.WaitGroup
	var successes atomic.Int32
	start := make(chan struct{})
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			body := fmt.Sprintf(`{"Alias":"same","Email":"user%d@example.test","Name":"Example"}`, i)
			w := httptest.NewRecorder()
			s.createUser(w, httptest.NewRequest("POST", "/netgo/v2/admin/users", strings.NewReader(body)))
			if w.Code == 201 {
				successes.Add(1)
			} else if w.Code != 409 {
				t.Errorf("unexpected status %d: %s", w.Code, w.Body.String())
			}
		}()
	}
	close(start)
	wg.Wait()
	st, err := db.View()
	if err != nil {
		t.Fatal(err)
	}
	if successes.Load() != 1 || calls.Load() != 1 || len(st.Users) != 1 {
		t.Fatalf("successes=%d upstream calls=%d users=%d", successes.Load(), calls.Load(), len(st.Users))
	}
}
