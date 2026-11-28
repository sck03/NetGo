package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"netgo/internal/model"
	"strings"
	"time"
	"unicode/utf8"
)

const adminPageSize = 50

func validSearch(after, search string) bool {
	return len(after) <= 128 && len(search) <= 512 && utf8.ValidString(search) && utf8.RuneCountInString(search) <= 128
}

// AdminRemotePage loads hosts and live-session dependencies. Grant administration
// has its own page, so a popular host cannot expand every inventory poll.
func (s *Store) AdminRemotePage(ctx context.Context, c model.Credential, after, search string) (model.State, string, error) {
	if !validSearch(after, search) {
		return model.State{}, "", errors.New("invalid_remote_page")
	}
	var next string
	st, err := s.viewWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err != nil {
			return st, err
		}
		err = readRows(tx, "hosts", st.Hosts,
			"WHERE id>? AND instr(lower(coalesce(json_extract(data,'$.name'),'')||' '||coalesce(json_extract(data,'$.platform'),'')),lower(?))>0 ORDER BY id LIMIT ?",
			after, strings.TrimSpace(search), adminPageSize+1)
		if err != nil {
			return st, err
		}
		next = trimPage(st.Hosts, adminPageSize)
		ids := make([]string, 0, len(st.Hosts))
		for id := range st.Hosts {
			ids = append(ids, id)
		}
		// Only active sessions enter the live inventory.
		raw, _ := json.Marshal(ids)
		if err = readRows(tx, "remote_sessions", st.RemoteSessions, "WHERE state IN ('issued','active') AND host_id IN (SELECT value FROM json_each(?))", string(raw)); err != nil {
			return st, err
		}
		err = remoteDependencies(tx, &st, c)
		return st, err
	})
	return st, next, err
}

type UserChoice struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
	Name  string `json:"name"`
}
type UserChoices struct {
	Users     []UserChoice `json:"users"`
	NextAfter string       `json:"next_after"`
}

// Search choices never require loading the complete account permission records.
func (s *Store) UserChoices(after, search string) (UserChoices, error) {
	page := UserChoices{Users: []UserChoice{}}
	if !validSearch(after, search) {
		return page, errors.New("invalid_user_page")
	}
	rows, err := s.reader.Query(`SELECT id,json_extract(data,'$.alias'),coalesce(json_extract(data,'$.name'),'') FROM users
        WHERE id>? AND coalesce(json_extract(data,'$.delete_pending'),0)=0
        AND coalesce(json_extract(data,'$.disabled'),0)=0
        AND (coalesce(json_extract(data,'$.expires_at'),'0001-01-01T00:00:00Z')='0001-01-01T00:00:00Z'
             OR julianday(json_extract(data,'$.expires_at'))>julianday(?)) AND
        instr(lower(coalesce(json_extract(data,'$.alias'),'')||' '||coalesce(json_extract(data,'$.name'),'')),lower(?))>0
        ORDER BY id LIMIT ?`, after, time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(search), adminPageSize+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var u UserChoice
		if err = rows.Scan(&u.ID, &u.Alias, &u.Name); err != nil {
			return page, err
		}
		if len(page.Users) == adminPageSize {
			page.NextAfter = page.Users[len(page.Users)-1].ID
			break
		}
		page.Users = append(page.Users, u)
	}
	return page, rows.Err()
}
