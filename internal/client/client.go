// Package client implements the otun tunnel client.
package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/bc183/otun/internal/protocol"
	"github.com/bc183/otun/internal/proxy"
	"github.com/charmbracelet/log"
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
	token      string

	session       *yamux.Session
	controlStream *protocol.ControlStream

	// Registration info received from server
	tunnelURL         string
	assignedSubdomain string

	// Reconnection settings
	backoffConfig BackoffConfig
	reconnect     bool
}

// New creates a new tunnel client.
func New(serverAddr, localAddr string) *Client {
	return &Client{
		serverAddr:    serverAddr,
		localAddr:     localAddr,
		backoffConfig: DefaultBackoffConfig(),
		reconnect:     true,
	}
}

// WithSubdomain sets a preferred subdomain for the tunnel.
func (c *Client) WithSubdomain(subdomain string) *Client {
	c.subdomain = subdomain
	return c
}

// WithToken sets the API key for authentication.
func (c *Client) WithToken(token string) *Client {
	c.token = token
	return c
}

// WithBackoff sets the backoff configuration for reconnection.
func (c *Client) WithBackoff(config BackoffConfig) *Client {
	c.backoffConfig = config
	return c
}

// WithReconnect enables or disables automatic reconnection.
func (c *Client) WithReconnect(enabled bool) *Client {
	c.reconnect = enabled
	return c
}

// WithMaxRetries sets the maximum number of reconnection attempts.
func (c *Client) WithMaxRetries(maxRetries int) *Client {
	c.backoffConfig.MaxRetries = maxRetries
	return c
}

// Run connects to the server and handles incoming streams.
// It returns when the connection is closed or the context is cancelled.
func (c *Client) Run(ctx context.Context) error {
	log.Debug("connecting to server", "server", c.serverAddr)

	// Connect to the tunnel server
	conn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to server %s: %w", c.serverAddr, err)
	}

	log.Debug("tcp connection established", "server", c.serverAddr)

	// Create yamux session (client side)
	session, err := yamux.Client(conn, nil)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to create yamux session: %w", err)
	}
	c.session = session

	// Watch for context cancellation and close session
	go func() {
		<-ctx.Done()
		session.Close()
	}()

	// Open Stream 0 (control stream)
	stream, err := session.OpenStream()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to open control stream: %w", err)
	}

	log.Debug("control stream opened", "stream_id", stream.StreamID())

	c.controlStream = protocol.NewControlStream(stream)

	// Send register message - use assigned subdomain if reconnecting
	subdomain := c.subdomain
	if c.assignedSubdomain != "" {
		subdomain = c.assignedSubdomain
	}
	if err := c.controlStream.SendRegister(subdomain, c.token); err != nil {
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
		log.Info("Tunnel ready!", "url", c.tunnelURL)
	case *protocol.ErrorMessage:
		session.Close()
		return fmt.Errorf("registration failed: %s", m.Message)
	default:
		session.Close()
		return fmt.Errorf("unexpected message type: %T", msg)
	}

	// Start heartbeat sender
	go c.sendHeartbeats(ctx)

	log.Info("Forwarding requests", "to", c.localAddr)

	// Accept and handle streams from the server
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return ErrShutdown
			}
			log.Debug("failed to accept stream", "error", err)
			return fmt.Errorf("session closed: %w", err)
		}

		log.Debug("accepted stream from server", "stream_id", stream.StreamID())

		// Handle each stream concurrently
		go c.handleStream(stream)
	}
}

// sendHeartbeats sends periodic heartbeat messages to the server.
// On failure, it closes the session to signal the main loop.
func (c *Client) sendHeartbeats(ctx context.Context) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.controlStream.SendHeartbeat(); err != nil {
				log.Debug("failed to send heartbeat, closing session", "error", err)
				c.session.Close()
				return
			}
			log.Debug("heartbeat sent")
		}
	}
}

// RunWithReconnect runs the client with automatic reconnection on transient failures.
func (c *Client) RunWithReconnect(ctx context.Context) error {
	if !c.reconnect {
		return c.Run(ctx)
	}

	backoff := NewBackoff(c.backoffConfig)

	for {
		// Clear tunnelURL to detect successful registration
		c.tunnelURL = ""

		err := c.Run(ctx)

		// If we connected successfully before failing, reset backoff
		if c.tunnelURL != "" {
			backoff.Reset()
		}

		// Exit on clean shutdown or permanent errors
		if err == nil || isPermanentError(err) {
			return err
		}

		// Check if max retries exceeded
		if backoff.MaxRetriesReached() {
			log.Error("max reconnection attempts reached")
			return ErrMaxRetriesExceeded
		}

		delay := backoff.NextDelay()
		log.Warn("connection lost, reconnecting...",
			"error", err,
			"attempt", backoff.Attempt(),
			"delay", delay.Round(time.Millisecond),
		)

		select {
		case <-ctx.Done():
			return ErrShutdown
		case <-time.After(delay):
		}

		log.Info("attempting to reconnect",
			"server", c.serverAddr,
			"subdomain", c.assignedSubdomain,
		)
	}
}

// handleStream handles a single stream by proxying it to the local service.
func (c *Client) handleStream(stream *yamux.Stream) {
	// Read only the first line to log the request (e.g., "GET /path HTTP/1.1")
	reader := bufio.NewReader(stream)
	requestLine, err := reader.ReadString('\n')
	if err == nil {
		method, path := parseRequestLine(requestLine)
		if method != "" {
			log.Info("Request", "method", method, "path", path)
		}
	}

	// Connect to the local service
	localConn, err := net.Dial("tcp", c.localAddr)
	if err != nil {
		log.Error("failed to connect to local service", "error", err, "local", c.localAddr)
		stream.Close()
		return
	}

	log.Debug("connected to local service", "local", c.localAddr, "stream_id", stream.StreamID())

	// Write the request line we already read
	if _, err := localConn.Write([]byte(requestLine)); err != nil {
		log.Debug("failed to write request line to local", "error", err)
		localConn.Close()
		stream.Close()
		return
	}

	// Proxy remaining traffic: combine buffered data with stream
	streamReader := io.MultiReader(reader, stream)
	combinedStream := &readerConn{Reader: streamReader, Conn: stream}

	if err := proxy.Bidirectional(combinedStream, localConn); err != nil {
		log.Debug("stream completed", "stream_id", stream.StreamID(), "error", err)
	} else {
		log.Debug("stream completed", "stream_id", stream.StreamID())
	}
}

// parseRequestLine extracts method and path from "GET /path HTTP/1.1\r\n"
func parseRequestLine(line string) (method, path string) {
	parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// readerConn wraps a Reader with a Conn for the proxy
type readerConn struct {
	io.Reader
	net.Conn
}

func (r *readerConn) Read(p []byte) (int, error) {
	return r.Reader.Read(p)
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
