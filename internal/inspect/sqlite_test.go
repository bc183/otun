package inspect

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func newTestSQLite(t *testing.T) *SQLiteStore {
	t.Helper()
	// Use a file DB (not :memory:) so it survives across SetMaxOpenConns(1)
	// behavior; use tmp dir for isolation.
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSQLiteStore_RoundTrip(t *testing.T) {
	s := newTestSQLite(t)
	r := &Record{
		ID:         NewID(),
		ReceivedAt: time.Now().Truncate(time.Nanosecond),
		Subdomain:  "abc",
		Host:       "abc.localhost",
		Method:     "POST",
		Path:       "/api/thing",
		Proto:      "HTTP/1.1",
		ReqHeaders: http.Header{"Content-Type": []string{"application/json"}},
		ReqBody:    []byte(`{"x":1}`),
		ReqSize:    7,
		Status:     201,
		RespHeaders: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"abc"},
		},
		RespBody:   []byte(`{"ok":true}`),
		RespSize:   11,
		DurationMs: 42,
	}
	if err := s.Add(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.Path != "/api/thing" {
		t.Errorf("method/path mismatch: %+v", got)
	}
	if string(got.ReqBody) != `{"x":1}` || string(got.RespBody) != `{"ok":true}` {
		t.Errorf("body mismatch")
	}
	if got.ReqHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("req header lost: %v", got.ReqHeaders)
	}
	if got.RespHeaders.Get("X-Request-Id") != "abc" {
		t.Errorf("resp header lost: %v", got.RespHeaders)
	}
	if got.DurationMs != 42 {
		t.Errorf("duration lost: %d", got.DurationMs)
	}
}

func TestSQLiteStore_FilterAndCursor(t *testing.T) {
	s := newTestSQLite(t)

	// Deterministic IDs so cursor comparisons are stable.
	for _, tc := range []struct {
		id     string
		method string
		path   string
		status int
	}{
		{"0000000000A", "GET", "/api/users", 200},
		{"0000000000B", "POST", "/api/users", 201},
		{"0000000000C", "GET", "/api/posts", 404},
		{"0000000000D", "GET", "/health", 500},
	} {
		if err := s.Add(context.Background(), &Record{
			ID: tc.id, Method: tc.method, Path: tc.path, Status: tc.status, ReceivedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.List(context.Background(), Filter{Method: "get"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("method filter: want 3, got %d", len(got))
	}

	got, err = s.List(context.Background(), Filter{StatusClass: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != 500 {
		t.Errorf("status class filter: %+v", got)
	}

	got, err = s.List(context.Background(), Filter{PathSub: "api/users"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("path sub filter: want 2, got %d", len(got))
	}

	got, err = s.List(context.Background(), Filter{Cursor: "0000000000C"})
	if err != nil {
		t.Fatal(err)
	}
	// Strictly older than C (lex-sorted): B, A (newest first among <C)
	if len(got) != 2 || got[0].ID != "0000000000B" {
		t.Errorf("cursor: %+v", got)
	}
}

func TestSQLiteStore_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")

	s1, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Add(context.Background(), &Record{ID: "persistent", Method: "GET", Path: "/", ReceivedAt: time.Now()})
	s1.Close()

	s2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.Get(context.Background(), "persistent")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Method != "GET" {
		t.Errorf("data lost on reopen")
	}
}
