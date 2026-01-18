// Package client implements the otun tunnel client.
package client

import (
	"bufio"
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

	session       *yamux.Session
	controlStream *protocol.ControlStream

	// Registration info received from server
	tunnelURL         string
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

	// Open Stream 0 (control stream)
	stream, err := session.OpenStream()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to open control stream: %w", err)
	}

	log.Debug("control stream opened", "stream_id", stream.StreamID())

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
		log.Info("Tunnel ready!", "url", c.tunnelURL)
	case *protocol.ErrorMessage:
		session.Close()
		return fmt.Errorf("registration failed: %s", m.Message)
	default:
		session.Close()
		return fmt.Errorf("unexpected message type: %T", msg)
	}

	// Start heartbeat sender
	go c.sendHeartbeats()

	log.Info("Forwarding requests", "to", c.localAddr)

	// Accept and handle streams from the server
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			log.Debug("failed to accept stream", "error", err)
			return fmt.Errorf("session closed: %w", err)
		}

		log.Debug("accepted stream from server", "stream_id", stream.StreamID())

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
			log.Debug("failed to send heartbeat", "error", err)
			return
		}
		log.Debug("heartbeat sent")

		// Read heartbeat ack (non-blocking would be better, but keeping simple for now)
		// The ack will be read in a separate goroutine if we want to handle it
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
