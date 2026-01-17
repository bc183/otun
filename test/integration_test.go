package test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bc183/otun/internal/client"
	"github.com/bc183/otun/internal/server"
)

// startLocalServer starts a simple HTTP server for testing
func startLocalServer(t *testing.T, addr string) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from local service!\nPath: %s\nMethod: %s\n", r.URL.Path, r.Method)
	})

	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	})

	mux.HandleFunc("/hash", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hash := sha256.Sum256(body)
		fmt.Fprintf(w, "size=%d\nhash=%s\n", len(body), hex.EncodeToString(hash[:]))
	})

	srv := &http.Server{Addr: addr, Handler: mux}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen on %s: %v", addr, err)
	}

	go srv.Serve(listener)

	return srv
}

// waitForPort waits for a port to be available
func waitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func TestTunnelIntegration(t *testing.T) {
	// Use random ports to avoid conflicts
	localAddr := "127.0.0.1:13000"
	controlAddr := "127.0.0.1:14443"
	publicAddr := "127.0.0.1:18080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr)
	defer localServer.Close()

	if err := waitForPort(localAddr, 2*time.Second); err != nil {
		t.Fatalf("local server not ready: %v", err)
	}
	t.Log("Local server started")

	// Start tunnel server
	srv := server.New(controlAddr, publicAddr)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}
	t.Log("Tunnel server started")

	// Start tunnel client
	cli := client.New(controlAddr, localAddr)
	go func() {
		if err := cli.Run(); err != nil {
			t.Logf("client error: %v", err)
		}
	}()

	// Give client time to connect
	time.Sleep(500 * time.Millisecond)
	t.Log("Tunnel client started")

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	t.Run("basic GET request", func(t *testing.T) {
		resp, err := httpClient.Get("http://" + publicAddr + "/")
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "Hello from local service") {
			t.Errorf("unexpected response: %s", body)
		}
	})

	t.Run("POST with data", func(t *testing.T) {
		resp, err := httpClient.Post("http://"+publicAddr+"/echo", "text/plain", strings.NewReader("test data"))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if string(body) != "test data" {
			t.Errorf("expected 'test data', got '%s'", body)
		}
	})

	t.Run("large payload", func(t *testing.T) {
		data := strings.Repeat("A", 10240)
		expectedHash := sha256.Sum256([]byte(data))

		resp, err := httpClient.Post("http://"+publicAddr+"/hash", "text/plain", strings.NewReader(data))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "size=10240") {
			t.Errorf("unexpected size in response: %s", body)
		}
		if !strings.Contains(string(body), hex.EncodeToString(expectedHash[:])) {
			t.Errorf("hash mismatch in response: %s", body)
		}
	})

	t.Run("concurrent requests", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan bool, 5)

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				resp, err := httpClient.Get(fmt.Sprintf("http://%s/?req=%d", publicAddr, n))
				if err != nil {
					t.Logf("concurrent request %d failed: %v", n, err)
					results <- false
					return
				}
				defer resp.Body.Close()

				body, _ := io.ReadAll(resp.Body)
				if strings.Contains(string(body), "Hello from local service") {
					results <- true
				} else {
					t.Logf("concurrent request %d unexpected response: %s", n, body)
					results <- false
				}
			}(i)
		}

		wg.Wait()
		close(results)

		successCount := 0
		for success := range results {
			if success {
				successCount++
			}
		}

		if successCount != 5 {
			t.Errorf("only %d/5 concurrent requests succeeded", successCount)
		}
	})

	t.Run("rapid sequential requests", func(t *testing.T) {
		successCount := 0
		for i := range 10 {
			resp, err := httpClient.Get("http://" + publicAddr + "/")
			if err != nil {
				t.Logf("rapid request %d failed: %v", i, err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if strings.Contains(string(body), "Hello from local service") {
				successCount++
			}
		}

		if successCount != 10 {
			t.Errorf("only %d/10 rapid requests succeeded", successCount)
		}
	})
}
