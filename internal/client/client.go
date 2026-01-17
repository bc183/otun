// Package client implements the otun tunnel client.
package client

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/bc183/otun/internal/protocol"
	"github.com/bc183/otun/internal/proxy"
	"github.com/hashicorp/yamux"
)

const (
	// HeartbeatInterval is how often to send heartbeat messages.
	HeartbeatInterval = 30 * time.Second
)

// Client is the otun tunnel client.
type Client struct {
	serverAddr string
	localAddr  string
	subdomain  string

	session       *yamux.Session
	controlStream *protocol.ControlStream

	// Registration info received from server
	tunnelURL       string
	assignedSubdomain string
}

// New creates a new tunnel client.
func New(serverAddr, localAddr string) *Client {
	return &Client{
		serverAddr: serverAddr,
		localAddr:  localAddr,
	}
}

// WithSubdomain sets a preferred subdomain for the tunnel.
func (c *Client) WithSubdomain(subdomain string) *Client {
	c.subdomain = subdomain
	return c
}

// Run connects to the server and handles incoming streams.
func (c *Client) Run() error {
	slog.Info("connecting to server", "server", c.serverAddr)

	// Connect to the tunnel server
	conn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to server %s: %w", c.serverAddr, err)
	}

	slog.Info("connected to server", "server", c.serverAddr)

	// Create yamux session (client side)
	session, err := yamux.Client(conn, nil)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to create yamux session: %w", err)
	}
	c.session = session

	// Open Stream 0 (control stream)
	stream, err := session.OpenStream()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to open control stream: %w", err)
	}

	slog.Info("control stream opened", "stream_id", stream.StreamID())

	c.controlStream = protocol.NewControlStream(stream)

	// Send register message
	if err := c.controlStream.SendRegister(c.subdomain); err != nil {
		session.Close()
		return fmt.Errorf("failed to send register message: %w", err)
	}

	// Wait for registered message
	msg, err := c.controlStream.ReadMessage()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to read registered message: %w", err)
	}

	switch m := msg.(type) {
	case *protocol.RegisteredMessage:
		c.tunnelURL = m.URL
		c.assignedSubdomain = m.Subdomain
		slog.Info("tunnel registered", "url", c.tunnelURL, "subdomain", c.assignedSubdomain)
	case *protocol.ErrorMessage:
		session.Close()
		return fmt.Errorf("registration failed: %s", m.Message)
	default:
		session.Close()
		return fmt.Errorf("unexpected message type: %T", msg)
	}

	// Start heartbeat sender
	go c.sendHeartbeats()

	slog.Info("ready to accept connections", "local", c.localAddr)

	// Accept and handle streams from the server
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			slog.Error("failed to accept stream", "error", err)
			return fmt.Errorf("session closed: %w", err)
		}

		slog.Info("accepted stream from server", "stream_id", stream.StreamID())

		// Handle each stream concurrently
		go c.handleStream(stream)
	}
}

// sendHeartbeats sends periodic heartbeat messages to the server.
func (c *Client) sendHeartbeats() {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for range ticker.C {
		if err := c.controlStream.SendHeartbeat(); err != nil {
			slog.Error("failed to send heartbeat", "error", err)
			return
		}
		slog.Debug("heartbeat sent")

		// Read heartbeat ack (non-blocking would be better, but keeping simple for now)
		// The ack will be read in a separate goroutine if we want to handle it
	}
}

// handleStream handles a single stream by proxying it to the local service.
func (c *Client) handleStream(stream *yamux.Stream) {
	// Connect to the local service
	localConn, err := net.Dial("tcp", c.localAddr)
	if err != nil {
		slog.Error("failed to connect to local service", "error", err, "local", c.localAddr)
		stream.Close()
		return
	}

	slog.Info("connected to local service", "local", c.localAddr, "stream_id", stream.StreamID())

	// Proxy traffic between stream and local service
	if err := proxy.Bidirectional(stream, localConn); err != nil {
		slog.Info("stream completed", "stream_id", stream.StreamID(), "error", err)
	} else {
		slog.Info("stream completed", "stream_id", stream.StreamID())
	}
}

// Close closes the client session.
func (c *Client) Close() error {
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

// TunnelURL returns the public URL for the tunnel.
func (c *Client) TunnelURL() string {
	return c.tunnelURL
}

// Subdomain returns the assigned subdomain.
func (c *Client) Subdomain() string {
	return c.assignedSubdomain
}
