package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func add(t *testing.T, s *Store, to, body string) Message {
	t.Helper()
	m, err := s.Add(Message{ID: NewSID("SM"), Direction: Outbound, To: to, Body: body, Status: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caduceus.db")
	s, err := Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := add(t, s, "+15551234567", "hello")
	s.Close()

	s, err = Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(want.ID)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
}

func TestTemporaryDatabaseIsDeletedOnClose(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	path := s.temp
	add(t, s, "+1555", "hi")
	s.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary database still exists: %v", err)
	}
}

func TestPrunesOldestBeyondMax(t *testing.T) {
	s, err := Open("", 3)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 1; i <= 5; i++ {
		add(t, s, "+1555", fmt.Sprint("message ", i))
	}
	got, err := s.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Body != "message 5" || got[2].Body != "message 3" {
		t.Fatalf("kept %+v", got)
	}
}

func TestListFilters(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	add(t, s, "+1 (555) 123-4567", "Your code is 1234")
	add(t, s, "+15559999999", "100% off_today")

	for name, c := range map[string]struct {
		f    Filter
		want int
	}{
		"by number":        {Filter{To: "15551234567"}, 1},
		"by text":          {Filter{Query: "CODE"}, 1},
		"literal percent":  {Filter{Query: "100%"}, 1},
		"percent no match": {Filter{Query: "%zzz"}, 0},
		"everything":       {Filter{}, 2},
		"limit":            {Filter{Limit: 1}, 1},
	} {
		got, err := s.List(c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: got %d messages (%v), want %d", name, len(got), err, c.want)
		}
	}
}

func TestSetStatusOnMissingMessage(t *testing.T) {
	s, err := Open("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.SetStatus("SMnope", "sent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
