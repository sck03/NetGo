package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"netgo/internal/model"
	"sort"
	"time"
)

// Summary counts rows inside the same snapshot as policy status without loading
// user, device or lease bodies into the overview poll.
func (s *Store) AdminSummary(now time.Time) (model.State, map[string]int, error) {
	counts := map[string]int{}
	st, err := s.viewWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err != nil {
			return st, err
		}
		var users, resources, devices, leases int
		err = tx.QueryRow(`SELECT (SELECT count(*) FROM users),
   (SELECT count(*) FROM resources WHERE json_extract(data,'$.protocol')<>'internet'),
   (SELECT count(*) FROM devices WHERE coalesce(json_extract(data,'$.client_type'),'')<>'admin'),
   (SELECT count(*) FROM vpn_leases l JOIN users u ON l.user_id=u.id WHERE
    julianday(json_extract(l.data,'$.expires_at'))>julianday(?1) AND coalesce(json_extract(u.data,'$.disabled'),0)=0 AND
    coalesce(json_extract(u.data,'$.delete_pending'),0)=0 AND (json_extract(u.data,'$.expires_at') LIKE '0001-%' OR julianday(json_extract(u.data,'$.expires_at'))>julianday(?1)))`, now.UTC().Format(time.RFC3339Nano)).Scan(&users, &resources, &devices, &leases)
		counts = map[string]int{"users": users, "resources": resources, "devices": devices, "leases": leases}
		return st, err
	})
	return st, counts, err
}

// Page lists share the state envelope but load only the page and the supporting
// options/labels needed to edit it. This is not a full policy snapshot.
func (s *Store) AdminPage(section, before string, limit int) (model.State, string, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	next := ""
	st, err := s.viewWith(func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err != nil {
			return st, err
		}
		switch section {
		case "users":
			err = readRows(tx, "users", st.Users, "WHERE id>? ORDER BY id LIMIT ?", before, limit+1)
			next = trimPage(st.Users, limit)
			if err == nil {
				err = readMap(tx, "resources", st.Resources)
			}
		case "resources":
			err = readRows(tx, "resources", st.Resources, "WHERE id>? ORDER BY id LIMIT ?", before, limit+1)
			next = trimPage(st.Resources, limit)
		case "sessions":
			err = readRows(tx, "devices", st.Devices, "WHERE id>? AND coalesce(json_extract(data,'$.client_type'),'')<>'admin' ORDER BY id LIMIT ?", before, limit+1)
			next = trimPage(st.Devices, limit)
			if err == nil {
				ids := make([]string, 0, len(st.Devices))
				for id := range st.Devices {
					ids = append(ids, id)
				}
				raw, _ := json.Marshal(ids)
				err = readRows(tx, "vpn_leases", st.Leases, "WHERE device_id IN (SELECT value FROM json_each(?))", string(raw))
			}
			if err == nil {
				ids := map[string]bool{}
				for _, d := range st.Devices {
					ids[d.UserID] = true
				}
				for _, l := range st.Leases {
					ids[l.UserID] = true
				}
				keys := make([]string, 0, len(ids))
				for id := range ids {
					keys = append(keys, id)
				}
				raw, _ := json.Marshal(keys)
				err = readRows(tx, "users", st.Users, "WHERE id IN (SELECT value FROM json_each(?))", string(raw))
			}
		case "remote":
			err = readMap(tx, "users", st.Users)
		default:
			return st, errors.New("invalid_admin_section")
		}
		return st, err
	})
	return st, next, err
}

func trimPage[T any](items map[string]T, limit int) string {
	if len(items) <= limit {
		return ""
	}
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids[limit:] {
		delete(items, id)
	}
	return ids[limit-1]
}
