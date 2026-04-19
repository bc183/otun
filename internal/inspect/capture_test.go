package inspect

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pipeTunnel returns two net.Conn endpoints connected over net.Pipe.
// client writes requests, server reads them; server writes responses.
func pipeTunnel(t *testing.T) (client, server net.Conn) {
	t.Helper()
	return net.Pipe()
}

// newLocalEcho spins up a local HTTP server that is what the client-side
// tunnel "forwards to". Returns host:port.
func newLocalEcho(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	u := strings.TrimPrefix(ts.URL, "http://")
	return u
}

func TestCapture_BasicRequestResponse(t *testing.T) {
	local := newLocalEcho(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, `{"got":%q}`, string(body))
	})

	store := NewMemoryStore(10)
	tunnelClient, tunnelServer := pipeTunnel(t)

	// Tunnel "server" side writes the incoming HTTP request.
	go func() {
		defer tunnelServer.Close()
		req, _ := http.NewRequest("POST", "http://abc.localhost/hello", strings.NewReader("hi there"))
		req.Header.Set("X-Test", "1")
		_ = req.Write(tunnelServer)
		// Read the response the capture path writes back
		_, _ = io.ReadAll(tunnelServer)
	}()

	err := Capture(context.Background(), tunnelClient, local, store, Options{
		MaxReqBody:  1024,
		MaxRespBody: 1024,
		Subdomain:   "abc",
	})
	if err != nil && err != io.EOF {
		t.Fatalf("capture: %v", err)
	}

	recs, _ := store.List(context.Background(), Filter{})
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r.Method != "POST" || r.Path != "/hello" {
		t.Errorf("method/path: %+v", r)
	}
	if r.Status != 201 {
		t.Errorf("status: %d", r.Status)
	}
	if string(r.ReqBody) != "hi there" {
		t.Errorf("req body: %q", r.ReqBody)
	}
	if !strings.Contains(string(r.RespBody), `"got":"hi there"`) {
		t.Errorf("resp body: %q", r.RespBody)
	}
	if r.ReqHeaders.Get("X-Test") != "1" {
		t.Errorf("req header lost")
	}
	if r.RespHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("resp header lost")
	}
	if r.Subdomain != "abc" {
		t.Errorf("subdomain: %q", r.Subdomain)
	}
	if r.DurationMs < 0 {
		t.Errorf("bad duration: %d", r.DurationMs)
	}
}

func TestCapture_RequestBodyTruncatedButFullyForwarded(t *testing.T) {
	var gotBodyLen int
	local := newLocalEcho(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBodyLen = len(body)
		w.WriteHeader(200)
	})

	store := NewMemoryStore(10)
	tunnelClient, tunnelServer := pipeTunnel(t)

	bigBody := strings.Repeat("a", 50_000)
	go func() {
		defer tunnelServer.Close()
		req, _ := http.NewRequest("POST", "http://x.localhost/upload", strings.NewReader(bigBody))
		_ = req.Write(tunnelServer)
		_, _ = io.ReadAll(tunnelServer)
	}()

	_ = Capture(context.Background(), tunnelClient, local, store, Options{
		MaxReqBody:  1000, // small cap
		MaxRespBody: 1024,
	})

	if gotBodyLen != 50_000 {
		t.Errorf("local service got %d bytes, want 50000 (truncation must not reach the service)", gotBodyLen)
	}
	recs, _ := store.List(context.Background(), Filter{})
	if len(recs) != 1 {
		t.Fatalf("no record")
	}
	r := recs[0]
	if r.ReqReason != BodyTooLarge {
		t.Errorf("want BodyTooLarge, got %q", r.ReqReason)
	}
	if len(r.ReqBody) != 1000 {
		t.Errorf("captured body len = %d, want 1000", len(r.ReqBody))
	}
	if r.ReqSize != 50_000 {
		t.Errorf("ReqSize = %d, want 50000", r.ReqSize)
	}
}

func TestCapture_UpgradeFallsBackToRaw(t *testing.T) {
	// Fake "WebSocket" local server: responds 101 then echoes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
			return
		}
		// Write 101 switching protocols
		c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
		// Echo any bytes
		buf := make([]byte, 1024)
		for {
			c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, err := c.Read(buf)
			if n > 0 {
				c.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	store := NewMemoryStore(10)
	tunnelClient, tunnelServer := pipeTunnel(t)

	done := make(chan []byte, 1)
	go func() {
		defer tunnelServer.Close()
		fmt.Fprintf(tunnelServer, "GET /ws HTTP/1.1\r\nHost: x.localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nFRAME1")
		var got []byte
		buf := make([]byte, 4096)
		for {
			tunnelServer.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
			n, err := tunnelServer.Read(buf)
			if n > 0 {
				got = append(got, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- got
	}()

	_ = Capture(context.Background(), tunnelClient, ln.Addr().String(), store, Options{
		MaxReqBody:  1024,
		MaxRespBody: 1024,
	})

	resp := <-done
	if !strings.Contains(string(resp), "101 Switching Protocols") {
		t.Errorf("expected 101 in response, got: %q", string(resp))
	}
	// FRAME1 should have been echoed
	if !strings.Contains(string(resp), "FRAME1") {
		t.Errorf("expected FRAME1 echoed, got: %q", string(resp))
	}

	recs, _ := store.List(context.Background(), Filter{})
	if len(recs) != 1 {
		t.Fatalf("no record")
	}
	if recs[0].ReqReason != BodyUpgrade {
		t.Errorf("want BodyUpgrade, got %q", recs[0].ReqReason)
	}
}

func TestCapture_SSEHeadersOnly(t *testing.T) {
	local := newLocalEcho(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: msg%d\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	store := NewMemoryStore(10)
	tunnelClient, tunnelServer := pipeTunnel(t)

	go func() {
		defer tunnelServer.Close()
		req, _ := http.NewRequest("GET", "http://x.localhost/events", nil)
		_ = req.Write(tunnelServer)
		_, _ = io.ReadAll(tunnelServer)
	}()

	_ = Capture(context.Background(), tunnelClient, local, store, Options{
		MaxReqBody:  1024,
		MaxRespBody: 1024,
	})

	recs, _ := store.List(context.Background(), Filter{})
	if len(recs) != 1 {
		t.Fatalf("no record")
	}
	if recs[0].RespReason != BodyStreamed {
		t.Errorf("want BodyStreamed, got %q", recs[0].RespReason)
	}
	if len(recs[0].RespBody) != 0 {
		t.Errorf("expected no captured body for SSE, got %d bytes", len(recs[0].RespBody))
	}
	if recs[0].RespHeaders.Get("Content-Type") != "text/event-stream" {
		t.Errorf("resp headers not captured")
	}
}
