package test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	srv := server.New(controlAddr, "", publicAddr, "", "", nil)
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
		if err := cli.Run(context.Background()); err != nil {
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
		// Request with just localhost (no subdomain) should get 400 Bad Request
		resp, err := makeRequest("GET", "http://"+publicAddr+"/", "localhost:18080", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", resp.StatusCode)
		}
	})

	t.Run("request to unknown subdomain rejected", func(t *testing.T) {
		// Request with unknown subdomain should get 404 Not Found
		resp, err := makeRequest("GET", "http://"+publicAddr+"/", "unknown.tunnel.localhost:18080", nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
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
	srv := server.New(controlAddr, "", publicAddr, "", "", nil)
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
		if err := clientA.Run(context.Background()); err != nil {
			t.Logf("client A error: %v", err)
		}
	}()

	// Start tunnel client B
	clientB := client.New(controlAddr, localAddrB).WithSubdomain(subdomainB)
	go func() {
		if err := clientB.Run(context.Background()); err != nil {
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

func TestClientGracefulShutdown(t *testing.T) {
	localAddr := "127.0.0.1:16000"
	controlAddr := "127.0.0.1:16443"
	publicAddr := "127.0.0.1:16080"
	subdomain := "shutdown"
	hostHeader := subdomain + ".tunnel.localhost:16080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "shutdown-service")
	defer localServer.Close()

	if err := waitForPort(localAddr, 2*time.Second); err != nil {
		t.Fatalf("local server not ready: %v", err)
	}

	// Start tunnel server
	srv := server.New(controlAddr, "", publicAddr, "", "", nil)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Track when client exits
	clientDone := make(chan error, 1)

	cli := client.New(controlAddr, localAddr).WithSubdomain(subdomain)
	go func() {
		clientDone <- cli.Run(ctx)
	}()

	// Wait for client to connect
	time.Sleep(500 * time.Millisecond)

	// Verify tunnel works
	resp, err := makeRequest("GET", "http://"+publicAddr+"/", hostHeader, nil)
	if err != nil {
		t.Fatalf("request failed before shutdown: %v", err)
	}
	resp.Body.Close()

	// Trigger graceful shutdown
	cancel()

	// Wait for client to exit
	select {
	case err := <-clientDone:
		if err != client.ErrShutdown {
			t.Errorf("expected ErrShutdown, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("client did not shut down within timeout")
	}
}

func TestClientReconnection(t *testing.T) {
	localAddr := "127.0.0.1:17000"
	controlAddr := "127.0.0.1:17443"
	publicAddr := "127.0.0.1:17080"
	subdomain := "reconnect"
	hostHeader := subdomain + ".tunnel.localhost:17080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "reconnect-service")
	defer localServer.Close()

	if err := waitForPort(localAddr, 2*time.Second); err != nil {
		t.Fatalf("local server not ready: %v", err)
	}

	// Create client with fast backoff BEFORE server is running
	// This tests that client will retry and connect once server starts
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientDone := make(chan error, 1)
	clientConnected := make(chan struct{}, 1)

	cli := client.New(controlAddr, localAddr).
		WithSubdomain(subdomain).
		WithBackoff(client.BackoffConfig{
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     500 * time.Millisecond,
			Multiplier:   2.0,
			Jitter:       0,
			MaxRetries:   10, // Allow enough retries for server to start
		})

	go func() {
		clientDone <- cli.RunWithReconnect(ctx)
	}()

	// Let client attempt to connect a few times (will fail since server isn't running)
	time.Sleep(300 * time.Millisecond)

	// Now start the tunnel server
	srv := server.New(controlAddr, "", publicAddr, "", "", nil)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	// Wait for client to eventually connect
	time.Sleep(1 * time.Second)

	// Verify tunnel works after client reconnected to newly started server
	resp, err := makeRequest("GET", "http://"+publicAddr+"/", hostHeader, nil)
	if err != nil {
		t.Fatalf("request failed after client reconnection: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "reconnect-service") {
		t.Errorf("unexpected response: %s", body)
	}

	// Clean shutdown
	cancel()
	select {
	case <-clientDone:
		// Good
	case <-time.After(2 * time.Second):
		t.Error("client did not shut down after reconnection test")
	}

	_ = clientConnected // suppress unused warning
}

func TestClientMaxRetriesExceeded(t *testing.T) {
	localAddr := "127.0.0.1:18000"
	controlAddr := "127.0.0.1:18443" // No server running on this port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientDone := make(chan error, 1)

	// Client with max 3 retries and fast backoff
	cli := client.New(controlAddr, localAddr).
		WithBackoff(client.BackoffConfig{
			InitialDelay: 50 * time.Millisecond,
			MaxDelay:     100 * time.Millisecond,
			Multiplier:   1.5,
			Jitter:       0,
			MaxRetries:   3,
		})

	go func() {
		clientDone <- cli.RunWithReconnect(ctx)
	}()

	// Should fail after 3 retries
	select {
	case err := <-clientDone:
		if err != client.ErrMaxRetriesExceeded {
			t.Errorf("expected ErrMaxRetriesExceeded, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("client did not exit after max retries")
	}
}

func TestClientNoReconnect(t *testing.T) {
	localAddr := "127.0.0.1:19000"
	controlAddr := "127.0.0.1:19443" // No server will be running

	clientDone := make(chan error, 1)

	// Client with reconnect disabled trying to connect to non-existent server
	cli := client.New(controlAddr, localAddr).
		WithReconnect(false)

	go func() {
		clientDone <- cli.RunWithReconnect(context.Background())
	}()

	// Client should exit immediately without retrying
	select {
	case err := <-clientDone:
		// Should get a connection error, not ErrMaxRetriesExceeded
		if err == client.ErrMaxRetriesExceeded {
			t.Error("client should not have retried with reconnect disabled")
		}
		if err == nil {
			t.Error("expected connection error, got nil")
		}
		// Any connection error is expected
	case <-time.After(2 * time.Second):
		t.Error("client did not exit promptly with reconnect disabled")
	}
}

func TestAuthenticationRequired(t *testing.T) {
	// Server with API keys configured - client without token should be rejected
	localAddr := "127.0.0.1:20000"
	controlAddr := "127.0.0.1:20443"
	publicAddr := "127.0.0.1:20080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "auth-service")
	defer localServer.Close()

	// Start tunnel server WITH API keys
	apiKeys := []string{"valid-key-1", "valid-key-2"}
	srv := server.New(controlAddr, "", publicAddr, "", "", apiKeys)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Client WITHOUT token - should be rejected
	cli := client.New(controlAddr, localAddr).
		WithSubdomain("notoken").
		WithReconnect(false)

	err := cli.Run(ctx)
	if err == nil {
		t.Fatal("expected authentication error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid or missing API key") {
		t.Errorf("expected 'invalid or missing API key' error, got: %v", err)
	}
}

func TestAuthenticationSuccess(t *testing.T) {
	// Server with API keys - client with valid token should connect
	localAddr := "127.0.0.1:21000"
	controlAddr := "127.0.0.1:21443"
	publicAddr := "127.0.0.1:21080"
	subdomain := "authenticated"
	hostHeader := subdomain + ".tunnel.localhost:21080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "auth-success-service")
	defer localServer.Close()

	// Start tunnel server WITH API keys
	apiKeys := []string{"secret-key-123", "another-key"}
	srv := server.New(controlAddr, "", publicAddr, "", "", apiKeys)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Client WITH valid token - should connect successfully
	cli := client.New(controlAddr, localAddr).
		WithSubdomain(subdomain).
		WithToken("secret-key-123").
		WithReconnect(false)

	clientDone := make(chan error, 1)
	go func() {
		clientDone <- cli.Run(ctx)
	}()

	// Wait for client to connect
	time.Sleep(500 * time.Millisecond)

	// Verify tunnel works
	resp, err := makeRequest("GET", "http://"+publicAddr+"/", hostHeader, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "auth-success-service") {
		t.Errorf("unexpected response: %s", body)
	}

	cancel()
}

func TestAuthenticationInvalidToken(t *testing.T) {
	// Server with API keys - client with wrong token should be rejected
	localAddr := "127.0.0.1:22000"
	controlAddr := "127.0.0.1:22443"
	publicAddr := "127.0.0.1:22080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "auth-invalid-service")
	defer localServer.Close()

	// Start tunnel server WITH API keys
	apiKeys := []string{"correct-key"}
	srv := server.New(controlAddr, "", publicAddr, "", "", apiKeys)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Client with WRONG token - should be rejected
	cli := client.New(controlAddr, localAddr).
		WithSubdomain("wrongtoken").
		WithToken("wrong-key").
		WithReconnect(false)

	err := cli.Run(ctx)
	if err == nil {
		t.Fatal("expected authentication error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid or missing API key") {
		t.Errorf("expected 'invalid or missing API key' error, got: %v", err)
	}
}

func TestAuthenticationWithMultipleKeys(t *testing.T) {
	// Server with multiple API keys - any valid key should work
	localAddr := "127.0.0.1:23000"
	controlAddr := "127.0.0.1:23443"
	publicAddr := "127.0.0.1:23080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "multi-key-service")
	defer localServer.Close()

	// Start tunnel server with multiple API keys
	apiKeys := []string{"key-alpha", "key-beta", "key-gamma"}
	srv := server.New(controlAddr, "", publicAddr, "", "", apiKeys)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	// Test each key works
	keys := []string{"key-alpha", "key-beta", "key-gamma"}
	for i, key := range keys {
		t.Run(fmt.Sprintf("key_%d", i), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			subdomain := fmt.Sprintf("client%d", i)
			hostHeader := subdomain + ".tunnel.localhost:23080"

			cli := client.New(controlAddr, localAddr).
				WithSubdomain(subdomain).
				WithToken(key).
				WithReconnect(false)

			clientDone := make(chan error, 1)
			go func() {
				clientDone <- cli.Run(ctx)
			}()

			// Wait for connection
			time.Sleep(300 * time.Millisecond)

			// Verify tunnel works
			resp, err := makeRequest("GET", "http://"+publicAddr+"/identity", hostHeader, nil)
			if err != nil {
				t.Fatalf("request failed with key %s: %v", key, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if !strings.Contains(string(body), "multi-key-service") {
				t.Errorf("unexpected response with key %s: %s", key, body)
			}

			cancel()
		})
	}
}

func TestConfigFileIntegration(t *testing.T) {
	// Test that config file values are used by the client
	localAddr := "127.0.0.1:24000"
	controlAddr := "127.0.0.1:24443"
	publicAddr := "127.0.0.1:24080"
	subdomain := "configtest"
	hostHeader := subdomain + ".tunnel.localhost:24080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "config-test-service")
	defer localServer.Close()

	// Start tunnel server with API key
	apiKeys := []string{"config-file-token"}
	srv := server.New(controlAddr, "", publicAddr, "", "", apiKeys)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := tmpDir + "/otun.yaml"
	configContent := fmt.Sprintf(`
server: %s
token: config-file-token
subdomain: %s
`, controlAddr, subdomain)

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Run the client binary with config file
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "../bin/otun", "http", localAddr, "-c", configPath)
	cmd.Env = append(os.Environ(), "HOME="+tmpDir) // Ensure we don't use user's config

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer cmd.Process.Kill()

	// Wait for client to connect
	time.Sleep(1 * time.Second)

	// Verify tunnel works - the config file should have provided the token
	resp, err := makeRequest("GET", "http://"+publicAddr+"/", hostHeader, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "config-test-service") {
		t.Errorf("unexpected response: %s", body)
	}
}

func TestConfigFileCLIOverride(t *testing.T) {
	// Test that CLI flags override config file values
	localAddr := "127.0.0.1:25000"
	controlAddr := "127.0.0.1:25443"
	publicAddr := "127.0.0.1:25080"
	subdomain := "clioverride"
	hostHeader := subdomain + ".tunnel.localhost:25080"

	// Start local HTTP server
	localServer := startLocalServer(t, localAddr, "cli-override-service")
	defer localServer.Close()

	// Start tunnel server with API key
	apiKeys := []string{"cli-override-token"}
	srv := server.New(controlAddr, "", publicAddr, "", "", apiKeys)
	go func() {
		if err := srv.Run(); err != nil {
			t.Logf("server error: %v", err)
		}
	}()

	if err := waitForPort(controlAddr, 2*time.Second); err != nil {
		t.Fatalf("tunnel server not ready: %v", err)
	}

	// Create a config file with WRONG token
	tmpDir := t.TempDir()
	configPath := tmpDir + "/otun.yaml"
	configContent := fmt.Sprintf(`
server: %s
token: wrong-token-in-config
subdomain: wrongsubdomain
`, controlAddr)

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Run the client binary with config file BUT override token and subdomain via CLI
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "../bin/otun", "http", localAddr,
		"-c", configPath,
		"-t", "cli-override-token", // Override the wrong token from config
		"-s", subdomain,            // Override subdomain
	)
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer cmd.Process.Kill()

	// Wait for client to connect
	time.Sleep(1 * time.Second)

	// Verify tunnel works - CLI token should have overridden config
	resp, err := makeRequest("GET", "http://"+publicAddr+"/", hostHeader, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "cli-override-service") {
		t.Errorf("unexpected response: %s", body)
	}
}
