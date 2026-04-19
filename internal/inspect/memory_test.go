package inspect

import (
	"context"
	"sync"
	"testing"
	"time"
)

func mustAdd(t *testing.T, s Store, r *Record) {
	t.Helper()
	if err := s.Add(context.Background(), r); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func TestMemoryStore_RingBufferEviction(t *testing.T) {
	s := NewMemoryStore(3)
	ids := []string{"a", "b", "c", "d", "e"}
	for _, id := range ids {
		mustAdd(t, s, &Record{ID: id, Method: "GET", Path: "/", ReceivedAt: time.Now()})
	}

	got, err := s.List(context.Background(), Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records, got %d", len(got))
	}
	// Newest first: e, d, c
	want := []string{"e", "d", "c"}
	for i, r := range got {
		if r.ID != want[i] {
			t.Errorf("index %d: want %q, got %q", i, want[i], r.ID)
		}
	}

	// Evicted "a" and "b" should not be Gettable
	if _, err := s.Get(context.Background(), "a"); err == nil {
		t.Error("expected error for evicted record")
	}
	if _, err := s.Get(context.Background(), "c"); err != nil {
		t.Errorf("expected c to exist: %v", err)
	}
}

func TestMemoryStore_Filter(t *testing.T) {
	s := NewMemoryStore(10)
	mustAdd(t, s, &Record{ID: "1", Method: "GET", Path: "/api/users", Status: 200})
	mustAdd(t, s, &Record{ID: "2", Method: "POST", Path: "/api/users", Status: 201})
	mustAdd(t, s, &Record{ID: "3", Method: "GET", Path: "/api/posts", Status: 404})
	mustAdd(t, s, &Record{ID: "4", Method: "GET", Path: "/health", Status: 500})

	cases := []struct {
		name   string
		filter Filter
		wantN  int
	}{
		{"method", Filter{Method: "get"}, 3},
		{"status class", Filter{StatusClass: 5}, 1},
		{"path sub", Filter{PathSub: "api/users"}, 2},
		{"combined", Filter{Method: "GET", PathSub: "api"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.List(context.Background(), tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.wantN {
				t.Errorf("want %d, got %d records", tc.wantN, len(got))
			}
		})
	}
}

func TestMemoryStore_Cursor(t *testing.T) {
	s := NewMemoryStore(10)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		mustAdd(t, s, &Record{ID: id})
	}

	got, err := s.List(context.Background(), Filter{Cursor: "d", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// Want strictly older than "d": c, b, a (newest first)
	want := []string{"c", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("want %d, got %d", len(want), len(got))
	}
	for i, r := range got {
		if r.ID != want[i] {
			t.Errorf("index %d: want %q, got %q", i, want[i], r.ID)
		}
	}
}

func TestMemoryStore_SubscribeReceivesAdds(t *testing.T) {
	s := NewMemoryStore(10)
	ch, unsub := s.Subscribe()
	defer unsub()

	var wg sync.WaitGroup
	wg.Add(1)
	got := make([]string, 0, 3)
	go func() {
		defer wg.Done()
		for r := range ch {
			got = append(got, r.ID)
			if len(got) == 3 {
				return
			}
		}
	}()

	for _, id := range []string{"x", "y", "z"} {
		mustAdd(t, s, &Record{ID: id})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive all records")
	}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestMemoryStore_SubscribeSlowConsumerDrops(t *testing.T) {
	s := NewMemoryStore(1000)
	_, unsub := s.Subscribe()
	defer unsub()

	// Flood. With channel buffer 16 and no receiver, drops are expected
	// and must not block Add.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			mustAdd(t, s, &Record{ID: NewID()})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Add blocked on slow subscriber")
	}
}
