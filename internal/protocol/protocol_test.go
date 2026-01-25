package protocol

import (
	"io"
	"testing"
)

// mockStream wraps two io.Pipe connections for bidirectional communication.
type mockStream struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (m *mockStream) Read(p []byte) (int, error) {
	return m.reader.Read(p)
}

func (m *mockStream) Write(p []byte) (int, error) {
	return m.writer.Write(p)
}

func (m *mockStream) Close() error {
	m.reader.Close()
	m.writer.Close()
	return nil
}

// newMockStreamPair creates two connected mock streams for testing.
func newMockStreamPair() (*mockStream, *mockStream) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()

	stream1 := &mockStream{reader: r1, writer: w2}
	stream2 := &mockStream{reader: r2, writer: w1}

	return stream1, stream2
}

func TestControlStreamRegister(t *testing.T) {
	stream1, stream2 := newMockStreamPair()
	defer stream1.Close()
	defer stream2.Close()

	client := NewControlStream(stream1)
	server := NewControlStream(stream2)

	// Client sends register
	done := make(chan error)
	go func() {
		done <- client.SendRegister("mysubdomain", "testtoken")
	}()

	// Server reads register
	msg, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	<-done

	regMsg, ok := msg.(*RegisterMessage)
	if !ok {
		t.Fatalf("expected RegisterMessage, got %T", msg)
	}

	if regMsg.Type != TypeRegister {
		t.Errorf("expected type %s, got %s", TypeRegister, regMsg.Type)
	}

	if regMsg.Subdomain != "mysubdomain" {
		t.Errorf("expected subdomain 'mysubdomain', got '%s'", regMsg.Subdomain)
	}
}

func TestControlStreamRegistered(t *testing.T) {
	stream1, stream2 := newMockStreamPair()
	defer stream1.Close()
	defer stream2.Close()

	server := NewControlStream(stream1)
	client := NewControlStream(stream2)

	// Server sends registered
	done := make(chan error)
	go func() {
		done <- server.SendRegistered("http://abc123.tunnel.dev", "abc123")
	}()

	// Client reads registered
	msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	<-done

	regMsg, ok := msg.(*RegisteredMessage)
	if !ok {
		t.Fatalf("expected RegisteredMessage, got %T", msg)
	}

	if regMsg.URL != "http://abc123.tunnel.dev" {
		t.Errorf("expected url 'http://abc123.tunnel.dev', got '%s'", regMsg.URL)
	}

	if regMsg.Subdomain != "abc123" {
		t.Errorf("expected subdomain 'abc123', got '%s'", regMsg.Subdomain)
	}
}

func TestControlStreamHeartbeat(t *testing.T) {
	stream1, stream2 := newMockStreamPair()
	defer stream1.Close()
	defer stream2.Close()

	client := NewControlStream(stream1)
	server := NewControlStream(stream2)

	// Client sends heartbeat
	done := make(chan error)
	go func() {
		done <- client.SendHeartbeat()
	}()

	// Server reads heartbeat
	msg, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	<-done

	_, ok := msg.(*HeartbeatMessage)
	if !ok {
		t.Fatalf("expected HeartbeatMessage, got %T", msg)
	}

	// Server sends heartbeat ack
	go func() {
		done <- server.SendHeartbeatAck()
	}()

	// Client reads heartbeat ack
	msg, err = client.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	<-done

	_, ok = msg.(*HeartbeatAckMessage)
	if !ok {
		t.Fatalf("expected HeartbeatAckMessage, got %T", msg)
	}
}

func TestControlStreamError(t *testing.T) {
	stream1, stream2 := newMockStreamPair()
	defer stream1.Close()
	defer stream2.Close()

	server := NewControlStream(stream1)
	client := NewControlStream(stream2)

	// Server sends error
	done := make(chan error)
	go func() {
		done <- server.SendError("subdomain already taken")
	}()

	// Client reads error
	msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	<-done

	errMsg, ok := msg.(*ErrorMessage)
	if !ok {
		t.Fatalf("expected ErrorMessage, got %T", msg)
	}

	if errMsg.Message != "subdomain already taken" {
		t.Errorf("expected message 'subdomain already taken', got '%s'", errMsg.Message)
	}
}

func TestMessageConstructors(t *testing.T) {
	tests := []struct {
		name     string
		msg      any
		wantType string
	}{
		{"register", NewRegisterMessage("sub", "token"), TypeRegister},
		{"registered", NewRegisteredMessage("http://url", "sub"), TypeRegistered},
		{"heartbeat", NewHeartbeatMessage(), TypeHeartbeat},
		{"heartbeat_ack", NewHeartbeatAckMessage(), TypeHeartbeatAck},
		{"error", NewErrorMessage("oops"), TypeError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotType string
			switch m := tt.msg.(type) {
			case *RegisterMessage:
				gotType = m.Type
			case *RegisteredMessage:
				gotType = m.Type
			case *HeartbeatMessage:
				gotType = m.Type
			case *HeartbeatAckMessage:
				gotType = m.Type
			case *ErrorMessage:
				gotType = m.Type
			}

			if gotType != tt.wantType {
				t.Errorf("expected type %s, got %s", tt.wantType, gotType)
			}
		})
	}
}
