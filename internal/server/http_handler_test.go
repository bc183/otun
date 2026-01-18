package server

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// mockConn implements net.Conn for testing
type mockConn struct {
	reader *bytes.Reader
	writer *bytes.Buffer
	closed bool
}

func newMockConn(data []byte) *mockConn {
	return &mockConn{
		reader: bytes.NewReader(data),
		writer: &bytes.Buffer{},
	}
}

func (m *mockConn) Read(b []byte) (int, error) {
	return m.reader.Read(b)
}

func (m *mockConn) Write(b []byte) (int, error) {
	return m.writer.Write(b)
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestParseHTTPHost(t *testing.T) {
	tests := []struct {
		name       string
		request    string
		wantHost   string
		wantErr    bool
		wantReplay bool // whether the returned conn should replay the original bytes
	}{
		{
			name: "simple GET request",
			request: "GET / HTTP/1.1\r\n" +
				"Host: example.com\r\n" +
				"\r\n",
			wantHost:   "example.com",
			wantErr:    false,
			wantReplay: true,
		},
		{
			name: "host with port",
			request: "GET /path HTTP/1.1\r\n" +
				"Host: example.com:8080\r\n" +
				"\r\n",
			wantHost:   "example.com:8080",
			wantErr:    false,
			wantReplay: true,
		},
		{
			name: "subdomain host",
			request: "GET / HTTP/1.1\r\n" +
				"Host: abc123.tunnel.example.com\r\n" +
				"\r\n",
			wantHost:   "abc123.tunnel.example.com",
			wantErr:    false,
			wantReplay: true,
		},
		{
			name: "POST with body",
			request: "POST /api HTTP/1.1\r\n" +
				"Host: api.example.com\r\n" +
				"Content-Length: 13\r\n" +
				"\r\n" +
				"hello, world!",
			wantHost:   "api.example.com",
			wantErr:    false,
			wantReplay: true,
		},
		{
			name: "multiple headers",
			request: "GET / HTTP/1.1\r\n" +
				"User-Agent: test\r\n" +
				"Host: myhost.com\r\n" +
				"Accept: */*\r\n" +
				"\r\n",
			wantHost:   "myhost.com",
			wantErr:    false,
			wantReplay: true,
		},
		{
			name:       "invalid HTTP request",
			request:    "not a valid request",
			wantHost:   "",
			wantErr:    true,
			wantReplay: true,
		},
		{
			name:       "empty request",
			request:    "",
			wantHost:   "",
			wantErr:    true,
			wantReplay: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := newMockConn([]byte(tt.request))

			host, newConn, err := parseHTTPHost(conn)

			if (err != nil) != tt.wantErr {
				t.Errorf("parseHTTPHost() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if host != tt.wantHost {
				t.Errorf("parseHTTPHost() host = %q, want %q", host, tt.wantHost)
			}

			if tt.wantReplay && newConn != nil {
				// Verify that the returned connection replays the original bytes
				replayed, err := io.ReadAll(newConn)
				if err != nil {
					t.Errorf("failed to read from newConn: %v", err)
				}
				if string(replayed) != tt.request {
					t.Errorf("newConn replayed %q, want %q", replayed, tt.request)
				}
			}
		})
	}
}

func TestExtractSubdomain(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{
			name: "subdomain with domain and port",
			host: "abc123.tunnel.example.com:8080",
			want: "abc123",
		},
		{
			name: "subdomain with domain no port",
			host: "abc123.tunnel.example.com",
			want: "abc123",
		},
		{
			name: "subdomain with localhost and port",
			host: "myapp.localhost:8080",
			want: "myapp",
		},
		{
			name: "subdomain with localhost no port",
			host: "myapp.localhost",
			want: "myapp",
		},
		{
			name: "just localhost with port",
			host: "localhost:8080",
			want: "",
		},
		{
			name: "just localhost no port",
			host: "localhost",
			want: "",
		},
		{
			name: "IP address with port",
			host: "127.0.0.1:8080",
			want: "127",
		},
		{
			name: "empty string",
			host: "",
			want: "",
		},
		{
			name: "two part domain",
			host: "example.com",
			want: "example",
		},
		{
			name: "three part domain",
			host: "www.example.com",
			want: "www",
		},
		{
			name: "long subdomain chain",
			host: "a.b.c.d.example.com",
			want: "a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSubdomain(tt.host)
			if got != tt.want {
				t.Errorf("extractSubdomain(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestParsedConnRead(t *testing.T) {
	// Test that parsedConn correctly reads from the combined reader
	originalData := "GET / HTTP/1.1\r\nHost: test.example.com\r\n\r\nBody content here"
	conn := newMockConn([]byte(originalData))

	_, newConn, err := parseHTTPHost(conn)
	if err != nil {
		t.Fatalf("parseHTTPHost() error = %v", err)
	}

	// Read in chunks to simulate real usage
	buf := make([]byte, 10)
	var result []byte
	for {
		n, err := newConn.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
	}

	if string(result) != originalData {
		t.Errorf("Read result = %q, want %q", result, originalData)
	}
}
