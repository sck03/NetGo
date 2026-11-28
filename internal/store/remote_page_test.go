package store

import (
	"context"
	"fmt"
	"netgo/internal/model"
	"strings"
	"testing"
	"time"
)

func TestUserChoicesPagesAndSearch(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	name := strings.Repeat("名称", 30)
	if err = s.Update(func(st *model.State) error {
		for i := 0; i < 53; i++ {
			id := fmt.Sprintf("u%03d", i)
			st.Users[id] = model.User{ID: id, Alias: id, Name: name, DeletePending: i == 52}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := s.UserChoices(context.Background(), "", "")
	if err != nil || len(first.Users) != 50 || first.NextAfter != "u049" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	last, err := s.UserChoices(context.Background(), first.NextAfter, "")
	if err != nil || len(last.Users) != 2 || last.NextAfter != "" || last.Users[1].ID != "u051" {
		t.Fatalf("last page includes missing or deleted users: %+v %v", last, err)
	}
	matched, err := s.UserChoices(context.Background(), "", " U050 ")
	if err != nil || len(matched.Users) != 1 || matched.Users[0].Name != name {
		t.Fatalf("case-insensitive alias search: %+v %v", matched, err)
	}
	matched, err = s.UserChoices(context.Background(), "", name)
	if err != nil || len(matched.Users) != 50 {
		t.Fatalf("Unicode display name search: %+v %v", matched, err)
	}
	if _, err = s.UserChoices(context.Background(), "", strings.Repeat("名", 129)); err == nil {
		t.Fatal("oversized search accepted")
	}
}

func TestUserChoicesExcludeInactiveAccounts(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Update(func(st *model.State) error {
		st.Users["active"] = model.User{ID: "active", Alias: "active"}
		st.Users["disabled"] = model.User{ID: "disabled", Alias: "disabled", Disabled: true}
		st.Users["expired"] = model.User{ID: "expired", Alias: "expired", ExpiresAt: time.Now().Add(-time.Minute)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.UserChoices(context.Background(), "", "")
	if err != nil || len(page.Users) != 1 || page.Users[0].ID != "active" {
		t.Fatal(page, err)
	}
}
