// Package server implements the otun tunnel server.
package server

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/bc183/otun/internal/protocol"
	"github.com/bc183/otun/internal/proxy"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/acme/autocert"
)

const (
	// HeartbeatTimeout is how long to wait before considering a client dead.
	HeartbeatTimeout = 90 * time.Second
)

// tunnelClient represents a connected tunnel client.
type tunnelClient struct {
	subdomain     string
	session       *yamux.Session
	controlStream *protocol.ControlStream
	lastHeartbeat time.Time
}

// Server is the otun tunnel server.
type Server struct {
	controlAddr string
	httpsAddr   string
	httpAddr    string
	domain      string
	certDir     string

	controlListener net.Listener

	// mu protects the clients map
	mu      sync.RWMutex
	clients map[string]*tunnelClient // subdomain -> client
}

// New creates a new tunnel server.
func New(controlAddr, httpsAddr, httpAddr, domain, certDir string) *Server {
	return &Server{
		controlAddr: controlAddr,
		httpsAddr:   httpsAddr,
		httpAddr:    httpAddr,
		domain:      domain,
		certDir:     certDir,
		clients:     make(map[string]*tunnelClient),
	}
}

// Run starts the server and blocks until an error occurs.
func (s *Server) Run() error {
	// Start control listener for tunnel clients
	var err error
	s.controlListener, err = net.Listen("tcp", s.controlAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on control port %s: %w", s.controlAddr, err)
	}
	defer s.controlListener.Close()
	slog.Info("control listener started", "addr", s.controlListener.Addr())

	// Start accepting tunnel clients in a goroutine
	go s.acceptTunnelClients()

	// If no domain configured, run HTTP-only mode (for local testing)
	if s.domain == "" {
		return s.runHTTPOnly()
	}

	// Run with TLS
	return s.runWithTLS()
}

// runHTTPOnly runs the server without TLS (for local testing).
func (s *Server) runHTTPOnly() error {
	slog.Info("running in HTTP-only mode (no TLS)", "addr", s.httpAddr)

	server := &http.Server{
		Addr:    s.httpAddr,
		Handler: s,
	}

	return server.ListenAndServe()
}

// runWithTLS runs the server with automatic TLS via Let's Encrypt.
func (s *Server) runWithTLS() error {
	// Setup autocert manager
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(s.certDir),
		HostPolicy: s.hostPolicy,
	}

	// HTTPS server (HTTP/1.1 only - HTTP/2 doesn't support connection hijacking
	// which we need for bidirectional proxying and WebSocket support)
	httpsServer := &http.Server{
		Addr:    s.httpsAddr,
		Handler: s,
		TLSConfig: &tls.Config{
			GetCertificate: manager.GetCertificate,
			NextProtos:     []string{"http/1.1"},
		},
	}

	// HTTP server for ACME challenges and redirect
	httpServer := &http.Server{
		Addr:    s.httpAddr,
		Handler: manager.HTTPHandler(http.HandlerFunc(s.redirectToHTTPS)),
	}

	// Start HTTP server in background
	go func() {
		slog.Info("HTTP server started (ACME challenges + redirect)", "addr", s.httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	// Start HTTPS server
	slog.Info("HTTPS server started", "addr", s.httpsAddr, "domain", "*."+s.domain)
	return httpsServer.ListenAndServeTLS("", "")
}

// hostPolicy determines which domains we'll accept for TLS certificates.
// Only issues certs for subdomains that have active tunnels.
func (s *Server) hostPolicy(ctx context.Context, host string) error {
	subdomain := extractSubdomain(host)
	if subdomain == "" {
		return fmt.Errorf("invalid host: %s", host)
	}

	s.mu.RLock()
	_, exists := s.clients[subdomain]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no tunnel registered for subdomain: %s", subdomain)
	}

	slog.Info("allowing certificate for", "host", host, "subdomain", subdomain)
	return nil
}

// redirectToHTTPS redirects HTTP requests to HTTPS.
func (s *Server) redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	target := "https://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// ServeHTTP implements http.Handler to route incoming HTTP requests to tunnels.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	subdomain := extractSubdomain(host)

	if subdomain == "" {
		slog.Warn("no subdomain in request", "host", host)
		http.Error(w, "No subdomain specified", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	client := s.clients[subdomain]
	s.mu.RUnlock()

	if client == nil {
		slog.Warn("no tunnel found for subdomain", "subdomain", subdomain, "host", host)
		http.Error(w, fmt.Sprintf("No tunnel found for subdomain: %s", subdomain), http.StatusNotFound)
		return
	}

	// Open a new stream to the tunnel client
	stream, err := client.session.OpenStream()
	if err != nil {
		slog.Error("failed to open stream", "error", err)
		http.Error(w, "Failed to connect to tunnel", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	slog.Info("routing to tunnel", "subdomain", subdomain, "method", r.Method, "path", r.URL.Path)

	// Hijack the connection to get raw TCP access
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("response writer does not support hijacking")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	clientConn, buf, err := hijacker.Hijack()
	if err != nil {
		slog.Error("failed to hijack connection", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Write the original request to the tunnel stream
	if err := r.Write(stream); err != nil {
		slog.Error("failed to write request to tunnel", "error", err)
		return
	}

	// Check if there's buffered data from the hijack
	if buf.Reader.Buffered() > 0 {
		buffered := make([]byte, buf.Reader.Buffered())
		buf.Read(buffered)
		stream.Write(buffered)
	}

	// Proxy bidirectionally
	if err := proxy.Bidirectional(clientConn, stream); err != nil {
		slog.Debug("proxy completed", "error", err)
	} else {
		slog.Debug("proxy completed", "subdomain", subdomain)
	}
}

// acceptTunnelClients accepts tunnel client connections and creates yamux sessions.
func (s *Server) acceptTunnelClients() {
	for {
		conn, err := s.controlListener.Accept()
		if err != nil {
			slog.Error("failed to accept tunnel client", "error", err)
			continue
		}

		slog.Info("tunnel client connected", "remote_addr", conn.RemoteAddr())

		go s.handleTunnelClient(conn)
	}
}

// handleTunnelClient handles a new tunnel client connection.
func (s *Server) handleTunnelClient(conn net.Conn) {
	// Create yamux session (server side)
	session, err := yamux.Server(conn, nil)
	if err != nil {
		slog.Error("failed to create yamux session", "error", err)
		conn.Close()
		return
	}

	// Accept Stream 0 (control stream) from the client
	stream, err := session.AcceptStream()
	if err != nil {
		slog.Error("failed to accept control stream", "error", err)
		session.Close()
		return
	}

	slog.Info("control stream accepted", "stream_id", stream.StreamID())

	controlStream := protocol.NewControlStream(stream)

	// Read register message
	msg, err := controlStream.ReadMessage()
	if err != nil {
		slog.Error("failed to read register message", "error", err)
		controlStream.SendError("failed to read register message")
		session.Close()
		return
	}

	registerMsg, ok := msg.(*protocol.RegisterMessage)
	if !ok {
		slog.Error("expected register message", "got", fmt.Sprintf("%T", msg))
		controlStream.SendError("expected register message")
		session.Close()
		return
	}

	// Generate subdomain if not provided
	subdomain := registerMsg.Subdomain
	if subdomain == "" {
		subdomain = generateSubdomain()
	}

	// Check if subdomain is already in use
	s.mu.Lock()
	if _, exists := s.clients[subdomain]; exists {
		s.mu.Unlock()
		slog.Warn("subdomain already in use", "subdomain", subdomain)
		controlStream.SendError(fmt.Sprintf("subdomain '%s' is already in use", subdomain))
		session.Close()
		return
	}

	// Register the client
	client := &tunnelClient{
		subdomain:     subdomain,
		session:       session,
		controlStream: controlStream,
		lastHeartbeat: time.Now(),
	}
	s.clients[subdomain] = client
	s.mu.Unlock()

	slog.Info("tunnel registered", "subdomain", subdomain, "remote_addr", conn.RemoteAddr())

	// Build the URL for the client
	var url string
	if s.domain != "" {
		url = fmt.Sprintf("https://%s.%s", subdomain, s.domain)
	} else {
		url = fmt.Sprintf("http://%s.localhost%s", subdomain, s.httpAddr)
	}

	if err := controlStream.SendRegistered(url, subdomain); err != nil {
		slog.Error("failed to send registered message", "error", err)
		s.removeClient(subdomain)
		session.Close()
		return
	}

	// Handle control messages (heartbeats) in this goroutine
	s.handleControlStream(client)
}

// handleControlStream handles control messages from a client.
func (s *Server) handleControlStream(client *tunnelClient) {
	defer s.removeClient(client.subdomain)
	defer client.session.Close()

	for {
		msg, err := client.controlStream.ReadMessage()
		if err != nil {
			slog.Info("control stream closed", "subdomain", client.subdomain, "error", err)
			return
		}

		switch msg.(type) {
		case *protocol.HeartbeatMessage:
			client.lastHeartbeat = time.Now()
			slog.Debug("heartbeat received", "subdomain", client.subdomain)
			if err := client.controlStream.SendHeartbeatAck(); err != nil {
				slog.Error("failed to send heartbeat ack", "error", err)
				return
			}
		default:
			slog.Warn("unexpected message type", "type", fmt.Sprintf("%T", msg))
		}
	}
}

// removeClient removes a client from the registry.
func (s *Server) removeClient(subdomain string) {
	s.mu.Lock()
	delete(s.clients, subdomain)
	s.mu.Unlock()
	slog.Info("tunnel unregistered", "subdomain", subdomain)
}

// generateSubdomain generates a random 8-character alphanumeric subdomain.
func generateSubdomain() string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
