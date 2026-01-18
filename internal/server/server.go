// Package server implements the otun tunnel server.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/bc183/otun/internal/protocol"
	"github.com/bc183/otun/internal/proxy"
	"github.com/hashicorp/yamux"
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
	publicAddr  string
	baseURL     string

	controlListener net.Listener
	publicListener  net.Listener

	// mu protects the clients map
	mu      sync.RWMutex
	clients map[string]*tunnelClient // subdomain -> client
}

// New creates a new tunnel server.
func New(controlAddr, publicAddr string) *Server {
	return &Server{
		controlAddr: controlAddr,
		publicAddr:  publicAddr,
		baseURL:     fmt.Sprintf("http://%s", publicAddr),
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

	// Start public listener for incoming traffic
	s.publicListener, err = net.Listen("tcp", s.publicAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on public port %s: %w", s.publicAddr, err)
	}
	defer s.publicListener.Close()
	slog.Info("public listener started", "addr", s.publicListener.Addr())

	// Start accepting tunnel clients in a goroutine
	go s.acceptTunnelClients()

	// Accept and handle public connections
	for {
		publicConn, err := s.publicListener.Accept()
		if err != nil {
			slog.Error("failed to accept public connection", "error", err)
			continue
		}

		go s.handlePublicConnection(publicConn)
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

	// Send registered message
	url := fmt.Sprintf("%s", s.baseURL) // Phase 4 will add subdomain routing
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
		// Set read deadline for heartbeat timeout
		// Note: yamux streams don't support SetReadDeadline directly,
		// so we rely on the session's keepalive or manual timeout checking

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

// handlePublicConnection handles an incoming public connection by proxying
// it through a yamux stream to the tunnel client.
func (s *Server) handlePublicConnection(publicConn net.Conn) {
	slog.Info("public connection accepted", "remote_addr", publicConn.RemoteAddr())

	// Parse HTTP headers to get Host
	host, conn, err := parseHTTPHost(publicConn)
	if err != nil {
		slog.Warn("failed to parse HTTP request", "error", err)
		publicConn.Close()
		return
	}

	subdomain := extractSubdomain(host)
	if subdomain == "" {
		slog.Warn("no subdomain in request", "host", host)
		publicConn.Close()
		return
	}

	// Look up client by subdomain
	s.mu.RLock()
	client := s.clients[subdomain]
	s.mu.RUnlock()

	if client == nil {
		slog.Warn("no tunnel found for subdomain", "subdomain", subdomain, "host", host)
		publicConn.Close()
		return
	}

	// Open a new stream to the tunnel client
	stream, err := client.session.OpenStream()
	if err != nil {
		slog.Error("failed to open stream", "error", err)
		publicConn.Close()
		return
	}

	slog.Info("routing to tunnel", "stream_id", stream.StreamID(), "subdomain", client.subdomain, "host", host)

	// Proxy traffic between parsed connection and stream
	if err := proxy.Bidirectional(conn, stream); err != nil {
		slog.Info("proxy completed", "error", err)
	} else {
		slog.Info("proxy completed", "stream_id", stream.StreamID())
	}
}

// generateSubdomain generates a random 8-character alphanumeric subdomain.
func generateSubdomain() string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
