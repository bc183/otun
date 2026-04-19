package inspect

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplay_AgainstLocal(t *testing.T) {
	var hits int32
	var lastBody string
	var lastHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body, _ := io.ReadAll(r.Body)
		lastBody = string(body)
		lastHeader = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(202)
		fmt.Fprintf(w, "replayed-%d", atomic.LoadInt32(&hits))
	}))
	defer ts.Close()
	local := strings.TrimPrefix(ts.URL, "http://")

	store := NewMemoryStore(10)
	orig := &Record{
		ID:         NewID(),
		ReceivedAt: time.Now(),
		Host:       "abc.localhost",
		Method:     "POST",
		Path:       "/foo",
		ReqHeaders: http.Header{"X-Custom": []string{"yes"}},
		ReqBody:    []byte(`payload`),
	}
	_ = store.Add(context.Background(), orig)

	rp := &Replayer{
		Store:     store,
		LocalAddr: local,
		Options:   DefaultOptions(),
	}

	out, err := rp.Replay(context.Background(), orig.ID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected 1 hit on local, got %d", hits)
	}
	if lastBody != "payload" {
		t.Errorf("body not forwarded: %q", lastBody)
	}
	if lastHeader != "yes" {
		t.Errorf("header not forwarded: %q", lastHeader)
	}
	if out.Status != 202 {
		t.Errorf("status: %d", out.Status)
	}
	if string(out.RespBody) != "replayed-1" {
		t.Errorf("resp body: %q", out.RespBody)
	}
	if !out.Replay {
		t.Errorf("expected Replay=true")
	}
	if out.OfID != orig.ID {
		t.Errorf("OfID: %q want %q", out.OfID, orig.ID)
	}

	// Replay again — original id should still be referenced
	out2, err := rp.Replay(context.Background(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out2.OfID != orig.ID {
		t.Errorf("replay-of-replay should anchor to original, got %q", out2.OfID)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("expected 2 hits after second replay, got %d", hits)
	}

	// Verify both new records are in the store
	recs, _ := store.List(context.Background(), Filter{})
	replays := 0
	for _, r := range recs {
		if r.Replay {
			replays++
		}
	}
	if replays != 2 {
		t.Errorf("expected 2 replay records, got %d", replays)
	}
}
