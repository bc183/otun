package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bc183/otun/internal/inspect"
)

func seed(t *testing.T) *inspect.MemoryStore {
	t.Helper()
	s := inspect.NewMemoryStore(100)
	_ = s.Add(context.Background(), &inspect.Record{
		ID: "0000000001", Method: "GET", Path: "/users", Status: 200, ReceivedAt: time.Now(),
		ReqHeaders:  http.Header{"X-Test": []string{"1"}},
		RespHeaders: http.Header{"Content-Type": []string{"application/json"}},
		RespBody:    []byte(`{"ok":true}`),
	})
	_ = s.Add(context.Background(), &inspect.Record{
		ID: "0000000002", Method: "POST", Path: "/items", Status: 500, ReceivedAt: time.Now(),
	})
	return s
}

func newTestServer(t *testing.T, store inspect.Store) http.Handler {
	t.Helper()
	s, err := New(Config{Addr: "127.0.0.1:0", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.static))
	mux.HandleFunc("GET /api/requests", s.listRequests)
	mux.HandleFunc("GET /api/requests/{id}", s.getRequest)
	mux.HandleFunc("POST /api/requests/{id}/replay", s.replayRequest)
	return mux
}

func TestList_HTML(t *testing.T) {
	h := newTestServer(t, seed(t))
	req := httptest.NewRequest("GET", "/api/requests", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/users") || !strings.Contains(body, "/items") {
		t.Errorf("list missing records: %s", body)
	}
	if !strings.Contains(body, "GET") || !strings.Contains(body, "POST") {
		t.Errorf("methods missing")
	}
}

func TestList_JSON(t *testing.T) {
	h := newTestServer(t, seed(t))
	req := httptest.NewRequest("GET", "/api/requests", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out struct {
		Records []struct {
			ID     string
			Method string
		}
	}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 2 {
		t.Errorf("records: %+v", out)
	}
}

func TestList_Filters(t *testing.T) {
	h := newTestServer(t, seed(t))
	req := httptest.NewRequest("GET", "/api/requests?status=5", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, "/users") {
		t.Errorf("status filter not applied")
	}
	if !strings.Contains(body, "/items") {
		t.Errorf("500 record missing")
	}
}

func TestDetail_HTML(t *testing.T) {
	h := newTestServer(t, seed(t))
	req := httptest.NewRequest("GET", "/api/requests/0000000001", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "X-Test") {
		t.Errorf("request headers missing: %s", body)
	}
	if !strings.Contains(body, "ok") {
		t.Errorf("response body missing: %s", body)
	}
}

func TestReplay_HitsLocal(t *testing.T) {
	hit := 0
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(204)
	}))
	defer local.Close()
	localAddr := strings.TrimPrefix(local.URL, "http://")

	store := seed(t)
	s, err := New(Config{
		Addr:  "127.0.0.1:0",
		Store: store,
		Replayer: &inspect.Replayer{
			Store:     store,
			LocalAddr: localAddr,
			Options:   inspect.DefaultOptions(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/requests/{id}/replay", s.replayRequest)

	req := httptest.NewRequest("POST", "/api/requests/0000000001/replay", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if hit != 1 {
		t.Errorf("replay did not hit local, hits=%d", hit)
	}
}

func TestStaticAssets(t *testing.T) {
	h := newTestServer(t, seed(t))
	for _, path := range []string{"/static/htmx.min.js", "/static/alpine.min.js", "/static/app.css"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("%s: status %d", path, w.Code)
		}
		if _, err := io.Copy(io.Discard, w.Body); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}
