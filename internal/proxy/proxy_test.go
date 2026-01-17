package proxy

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// mockConn is a bidirectional in-memory connection that supports CloseWrite().
// It uses io.Pipe internally for each direction.
type mockConn struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

// mockConnPair creates two connected mock connections.
// Data written to conn1 can be read from conn2 and vice versa.
func mockConnPair() (*mockConn, *mockConn) {
	// Pipe for conn1 -> conn2 direction
	r1, w1 := io.Pipe()
	// Pipe for conn2 -> conn1 direction
	r2, w2 := io.Pipe()

	conn1 := &mockConn{
		reader: r2, // conn1 reads what conn2 writes
		writer: w1, // conn1 writes for conn2 to read
	}
	conn2 := &mockConn{
		reader: r1, // conn2 reads what conn1 writes
		writer: w2, // conn2 writes for conn1 to read
	}

	return conn1, conn2
}

func (c *mockConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *mockConn) Write(p []byte) (int, error) {
	return c.writer.Write(p)
}

func (c *mockConn) Close() error {
	c.reader.Close()
	c.writer.Close()
	return nil
}

// CloseWrite closes the write side, signaling EOF to the reader on the other end.
func (c *mockConn) CloseWrite() error {
	return c.writer.Close()
}

func TestBidirectional(t *testing.T) {
	t.Run("copies data in both directions", func(t *testing.T) {
		// Create two mock connection pairs
		// conn1a <--> conn1b (pair 1)
		// conn2a <--> conn2b (pair 2)
		//
		// Bidirectional(conn1b, conn2a) proxies between them.
		// Write to conn1a -> appears at conn2b (and vice versa).
		conn1a, conn1b := mockConnPair()
		conn2a, conn2b := mockConnPair()

		done := make(chan error, 1)
		go func() {
			done <- Bidirectional(conn1b, conn2a)
		}()

		// Write from conn1a, should appear at conn2b
		msg1 := []byte("hello from side 1")
		go func() {
			conn1a.Write(msg1)
		}()

		buf := make([]byte, len(msg1))
		_, err := io.ReadFull(conn2b, buf)
		if err != nil {
			t.Fatalf("failed to read from conn2b: %v", err)
		}
		if !bytes.Equal(buf, msg1) {
			t.Errorf("got %q, want %q", buf, msg1)
		}

		// Write from conn2b, should appear at conn1a
		msg2 := []byte("hello from side 2")
		go func() {
			conn2b.Write(msg2)
		}()

		buf = make([]byte, len(msg2))
		_, err = io.ReadFull(conn1a, buf)
		if err != nil {
			t.Fatalf("failed to read from conn1a: %v", err)
		}
		if !bytes.Equal(buf, msg2) {
			t.Errorf("got %q, want %q", buf, msg2)
		}

		// Close the outer connections to terminate the proxy
		conn1a.Close()
		conn2b.Close()

		// Wait for Bidirectional to complete
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Bidirectional returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Bidirectional did not complete in time")
		}
	})

	t.Run("closes both connections on completion", func(t *testing.T) {
		conn1a, conn1b := mockConnPair()
		conn2a, conn2b := mockConnPair()

		done := make(chan error, 1)
		go func() {
			done <- Bidirectional(conn1b, conn2a)
		}()

		// Close outer connections to signal EOF
		conn1a.Close()
		conn2b.Close()

		select {
		case <-done:
			// Bidirectional completed
		case <-time.After(time.Second):
			t.Fatal("Bidirectional did not complete in time")
		}

		// Verify inner connections are closed by attempting to write
		_, err := conn1b.Write([]byte("test"))
		if err == nil {
			t.Error("expected conn1b to be closed")
		}

		_, err = conn2a.Write([]byte("test"))
		if err == nil {
			t.Error("expected conn2a to be closed")
		}
	})

	t.Run("handles large data transfer", func(t *testing.T) {
		conn1a, conn1b := mockConnPair()
		conn2a, conn2b := mockConnPair()

		done := make(chan error, 1)
		go func() {
			done <- Bidirectional(conn1b, conn2a)
		}()

		// Send 1MB of data
		dataSize := 1024 * 1024
		data := make([]byte, dataSize)
		for i := range data {
			data[i] = byte(i % 256)
		}

		// Write in a goroutine
		go func() {
			conn1a.Write(data)
			conn1a.Close()
		}()

		// Read all data
		received, err := io.ReadAll(conn2b)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}

		if len(received) != dataSize {
			t.Errorf("received %d bytes, want %d", len(received), dataSize)
		}

		if !bytes.Equal(received, data) {
			t.Error("received data does not match sent data")
		}

		conn2b.Close()

		select {
		case <-done:
			// Success
		case <-time.After(5 * time.Second):
			t.Fatal("Bidirectional did not complete in time")
		}
	})
}

func TestFirstError(t *testing.T) {
	tests := []struct {
		name    string
		errs    []error
		wantNil bool
	}{
		{
			name:    "all nil returns nil",
			errs:    []error{nil, nil, nil},
			wantNil: true,
		},
		{
			name:    "EOF only returns nil",
			errs:    []error{io.EOF, nil, io.EOF},
			wantNil: true,
		},
		{
			name:    "returns first non-EOF error",
			errs:    []error{io.EOF, io.ErrClosedPipe, io.ErrUnexpectedEOF},
			wantNil: false,
		},
		{
			name:    "empty slice returns nil",
			errs:    []error{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := firstError(tt.errs...)
			if tt.wantNil && err != nil {
				t.Errorf("expected nil, got %v", err)
			}
			if !tt.wantNil && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
