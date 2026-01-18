package server

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
)

// parsedConn wraps a net.Conn after parsing HTTP headers.
// It replays the captured header bytes before reading from the connection.
type parsedConn struct {
	net.Conn
	reader io.Reader
}

func (p *parsedConn) Read(b []byte) (int, error) {
	return p.reader.Read(b)
}

// parseHTTPHost reads HTTP headers from the connection to extract the Host header.
// Returns the host, a new connection that replays the read bytes, and any error.
func parseHTTPHost(conn net.Conn) (host string, newConn net.Conn, err error) {
	// TeeReader writes everything read to the buffer
	var buf bytes.Buffer
	tee := io.TeeReader(conn, &buf)

	// Parse HTTP request - this reads headers only, not the body
	req, err := http.ReadRequest(bufio.NewReader(tee))
	if err != nil {
		// Even on error, return a conn that replays what we read
		combined := io.MultiReader(&buf, conn)
		return "", &parsedConn{Conn: conn, reader: combined}, err
	}

	host = req.Host

	// Combine: captured headers + body (still in req.Body which reads from tee/conn)
	// The buffer has the headers, and conn has the rest (body)
	combined := io.MultiReader(&buf, conn)

	return host, &parsedConn{Conn: conn, reader: combined}, nil
}

// extractSubdomain parses the Host header and extracts the subdomain.
// Expected formats:
//   - "abc123.tunnel.example.com" → "abc123"
//   - "abc123.tunnel.example.com:8080" → "abc123"
//   - "abc123.localhost" → "abc123"
//   - "abc123.localhost:8080" → "abc123"
//   - "localhost:8080" → "" (no subdomain)
func extractSubdomain(host string) string {
	// Remove port if present
	if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
		// Check it's not IPv6 (which has multiple colons)
		if strings.Count(host, ":") == 1 {
			host = host[:colonIdx]
		}
	}

	// Split by dots
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		// Just "localhost" or similar, no subdomain
		return ""
	}

	// First part is the subdomain
	return parts[0]
}
