package store

import (
	"context"
	"database/sql"
	"errors"
	"netgo/internal/model"
	"sort"
	"strings"
)

type ResourceChoices struct {
	Resources []model.Resource `json:"resources"`
	NextAfter string           `json:"next_after"`
}

func (s *Store) ResourceChoices(ctx context.Context, after, search string) (ResourceChoices, error) {
	page := ResourceChoices{Resources: []model.Resource{}}
	if !validSearch(after, search) {
		return page, errors.New("invalid_resource_page")
	}
	st, err := s.viewWithContext(ctx, func(tx *sql.Tx) (model.State, error) {
		st, err := read(tx, 0)
		if err == nil {
			err = readRows(tx, "resources", st.Resources, "WHERE id>? AND instr(lower(coalesce(json_extract(data,'$.name'),'')||' '||coalesce(json_extract(data,'$.cidr'),'')),lower(?))>0 ORDER BY id LIMIT ?", after, strings.TrimSpace(search), adminPageSize+1)
		}
		return st, err
	})
	if err != nil {
		return page, err
	}
	page.NextAfter = trimPage(st.Resources, adminPageSize)
	for _, r := range st.Resources {
		page.Resources = append(page.Resources, r)
	}
	sort.Slice(page.Resources, func(i, j int) bool { return page.Resources[i].ID < page.Resources[j].ID })
	return page, nil
}
