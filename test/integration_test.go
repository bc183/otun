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
func startLocalServer(t *testing.T, addr string, name string) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from %s!\nPath: %s\nMethod: %s\n", name, r.URL.Path, r.Method)
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

	mux.HandleFunc("/identity", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s", name)
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

// makeRequest makes an HTTP request with the specified Host header.
// We disable keep-alive to ensure each request gets a fresh TCP connection,
// which matches real-world behavior where different hostnames (subdomains)
// use separate connection pools.
//
// TODO: The server currently only parses Host on the first request per connection.
// If a client sends multiple requests with different Host headers on the same
// keep-alive connection, they'd all route to the first Host. To fix this,
// we'd need HTTP-aware request-by-request routing instead of raw TCP proxying.
func makeRequest(method, url, host string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Host = host
	req.Close = true // Disable keep-alive

	client := &http.Client{Timeout: 5 * time.Second}
	return client.Do(req)
}

func TestTunnelIntegration(t *testing.T) {
	// Use fixed ports for testing
	localAddr := "127.0.0.1:13000"
	controlAddr := "127.0.0.1:14443"
	publicAddr := "127.0.0.1:18080"
	subdomain := "test"
	hostHeader := subdomain + ".tunnel.localhost:18080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "local-service")
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

	// Start tunnel client with specific subdomain
	cli := client.New(controlAddr, localAddr).WithSubdomain(subdomain)
	go func() {
		if err := cli.Run(); err != nil {
			t.Logf("client error: %v", err)
		}
	}()

	// Give client time to connect and register
	time.Sleep(500 * time.Millisecond)
	t.Log("Tunnel client started")

	t.Run("basic GET request", func(t *testing.T) {
		resp, err := makeRequest("GET", "http://"+publicAddr+"/", hostHeader, nil)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "Hello from local-service") {
			t.Errorf("unexpected response: %s", body)
		}
	})

	t.Run("POST with data", func(t *testing.T) {
		resp, err := makeRequest("POST", "http://"+publicAddr+"/echo", hostHeader, strings.NewReader("test data"))
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

		resp, err := makeRequest("POST", "http://"+publicAddr+"/hash", hostHeader, strings.NewReader(data))
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
				resp, err := makeRequest("GET", fmt.Sprintf("http://%s/?req=%d", publicAddr, n), hostHeader, nil)
				if err != nil {
					t.Logf("concurrent request %d failed: %v", n, err)
					results <- false
					return
				}
				defer resp.Body.Close()

				body, _ := io.ReadAll(resp.Body)
				if strings.Contains(string(body), "Hello from local-service") {
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
			resp, err := makeRequest("GET", "http://"+publicAddr+"/", hostHeader, nil)
			if err != nil {
				t.Logf("rapid request %d failed: %v", i, err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if strings.Contains(string(body), "Hello from local-service") {
				successCount++
			}
		}

		if successCount != 10 {
			t.Errorf("only %d/10 rapid requests succeeded", successCount)
		}
	})

	t.Run("request without subdomain rejected", func(t *testing.T) {
		// Request with just localhost (no subdomain) should fail
		resp, err := makeRequest("GET", "http://"+publicAddr+"/", "localhost:18080", nil)
		if err == nil {
			resp.Body.Close()
			// Connection should be closed by server, resulting in empty response or error
			body, _ := io.ReadAll(resp.Body)
			if len(body) > 0 {
				t.Errorf("expected request to be rejected, got response: %s", body)
			}
		}
		// Error is expected - server closes connection for unknown subdomain
	})

	t.Run("request to unknown subdomain rejected", func(t *testing.T) {
		resp, err := makeRequest("GET", "http://"+publicAddr+"/", "unknown.tunnel.localhost:18080", nil)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if len(body) > 0 {
				t.Errorf("expected request to be rejected, got response: %s", body)
			}
		}
		// Error is expected - server closes connection for unknown subdomain
	})
}

func TestMultiClientRouting(t *testing.T) {
	// Ports for this test
	localAddrA := "127.0.0.1:15001"
	localAddrB := "127.0.0.1:15002"
	controlAddr := "127.0.0.1:15443"
	publicAddr := "127.0.0.1:15080"

	subdomainA := "clienta"
	subdomainB := "clientb"
	hostA := subdomainA + ".tunnel.localhost:15080"
	hostB := subdomainB + ".tunnel.localhost:15080"

	// Start two local HTTP servers with different identities
	localServerA := startLocalServer(t, localAddrA, "service-A")
	defer localServerA.Close()

	localServerB := startLocalServer(t, localAddrB, "service-B")
	defer localServerB.Close()

	if err := waitForPort(localAddrA, 2*time.Second); err != nil {
		t.Fatalf("local server A not ready: %v", err)
	}
	if err := waitForPort(localAddrB, 2*time.Second); err != nil {
		t.Fatalf("local server B not ready: %v", err)
	}
	t.Log("Local servers started")

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

	// Start tunnel client A
	clientA := client.New(controlAddr, localAddrA).WithSubdomain(subdomainA)
	go func() {
		if err := clientA.Run(); err != nil {
			t.Logf("client A error: %v", err)
		}
	}()

	// Start tunnel client B
	clientB := client.New(controlAddr, localAddrB).WithSubdomain(subdomainB)
	go func() {
		if err := clientB.Run(); err != nil {
			t.Logf("client B error: %v", err)
		}
	}()

	// Give clients time to connect and register
	time.Sleep(500 * time.Millisecond)
	t.Log("Tunnel clients started")

	t.Run("route to client A", func(t *testing.T) {
		resp, err := makeRequest("GET", "http://"+publicAddr+"/identity", hostA, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if string(body) != "service-A" {
			t.Errorf("expected 'service-A', got '%s'", body)
		}
	})

	t.Run("route to client B", func(t *testing.T) {
		resp, err := makeRequest("GET", "http://"+publicAddr+"/identity", hostB, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if string(body) != "service-B" {
			t.Errorf("expected 'service-B', got '%s'", body)
		}
	})

	t.Run("alternating requests", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			var host, expected string
			if i%2 == 0 {
				host = hostA
				expected = "service-A"
			} else {
				host = hostB
				expected = "service-B"
			}

			resp, err := makeRequest("GET", "http://"+publicAddr+"/identity", host, nil)
			if err != nil {
				t.Fatalf("request %d failed: %v", i, err)
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if string(body) != expected {
				t.Errorf("request %d: expected '%s', got '%s'", i, expected, body)
			}
		}
	})

	t.Run("concurrent multi-client requests", func(t *testing.T) {
		var wg sync.WaitGroup
		errCount := 0
		var mu sync.Mutex

		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()

				var host, expected string
				if n%2 == 0 {
					host = hostA
					expected = "service-A"
				} else {
					host = hostB
					expected = "service-B"
				}

				resp, err := makeRequest("GET", "http://"+publicAddr+"/identity", host, nil)
				if err != nil {
					t.Logf("request %d failed: %v", n, err)
					mu.Lock()
					errCount++
					mu.Unlock()
					return
				}

				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				if string(body) != expected {
					t.Logf("request %d: expected '%s', got '%s'", n, expected, body)
					mu.Lock()
					errCount++
					mu.Unlock()
				}
			}(i)
		}

		wg.Wait()

		if errCount > 0 {
			t.Errorf("%d/20 requests failed or misrouted", errCount)
		}
	})
}
