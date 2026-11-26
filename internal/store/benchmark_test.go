package store

import (
	"fmt"
	"netgo/internal/model"
	"testing"
	"time"
)

func BenchmarkReadPaths(b *testing.B) {
	db, e := Open(":memory:")
	if e != nil {
		b.Fatal(e)
	}
	defer db.Close()
	e = db.Update(func(st *model.State) error {
		for i := 0; i < 200; i++ {
			id := fmt.Sprint(i)
			st.Users[id] = model.User{ID: id, Alias: "user" + id, Resources: []string{"r"}}
			st.Devices[id] = model.Device{ID: id, UserID: id}
			st.Credentials[id] = model.Credential{UserID: id, DeviceID: id, ExpiresAt: time.Now().Add(time.Hour)}
		}
		st.Resources["r"] = model.Resource{ID: "r", Ports: []uint16{443}}
		for i := 0; i < 10000; i++ {
			st.Record("admin", "seed", "test")
		}
		return nil
	})
	if e != nil {
		b.Fatal(e)
	}
	b.Run("DomainSnapshot", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, e := db.View(); e != nil {
				b.Fatal(e)
			}
		}
	})
	b.Run("AdminSummary", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, e := db.AdminSummary(time.Now()); e != nil {
				b.Fatal(e)
			}
		}
	})
	b.Run("IndexedIdentity", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, _, e := db.Identity("100"); e != nil {
				b.Fatal(e)
			}
		}
	})
	b.Run("AuditPage100", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, e := db.Audit(0, 100); e != nil {
				b.Fatal(e)
			}
		}
	})
}
