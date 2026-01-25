package protocol

import (
	"encoding/json"
	"fmt"
	"io"
)

// ControlStream handles reading and writing control messages over a stream.
type ControlStream struct {
	encoder *json.Encoder
	decoder *json.Decoder
	stream  io.ReadWriteCloser
}

// NewControlStream creates a new control stream handler.
func NewControlStream(stream io.ReadWriteCloser) *ControlStream {
	return &ControlStream{
		encoder: json.NewEncoder(stream),
		decoder: json.NewDecoder(stream),
		stream:  stream,
	}
}

// SendRegister sends a register message.
func (c *ControlStream) SendRegister(subdomain, token string) error {
	return c.encoder.Encode(NewRegisterMessage(subdomain, token))
}

// SendRegistered sends a registered message.
func (c *ControlStream) SendRegistered(url, subdomain string) error {
	return c.encoder.Encode(NewRegisteredMessage(url, subdomain))
}

// SendHeartbeat sends a heartbeat message.
func (c *ControlStream) SendHeartbeat() error {
	return c.encoder.Encode(NewHeartbeatMessage())
}

// SendHeartbeatAck sends a heartbeat acknowledgment message.
func (c *ControlStream) SendHeartbeatAck() error {
	return c.encoder.Encode(NewHeartbeatAckMessage())
}

// SendError sends an error message.
func (c *ControlStream) SendError(message string) error {
	return c.encoder.Encode(NewErrorMessage(message))
}

// messageType is used to peek at the type field.
type messageType struct {
	Type string `json:"type"`
}

// ReadMessage reads and returns the next control message.
// Returns one of: *RegisterMessage, *RegisteredMessage, *HeartbeatMessage,
// *HeartbeatAckMessage, or *ErrorMessage.
func (c *ControlStream) ReadMessage() (any, error) {
	// Decode into raw JSON first to peek at type
	var raw json.RawMessage
	if err := c.decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to read message: %w", err)
	}

	// Get the type
	var mt messageType
	if err := json.Unmarshal(raw, &mt); err != nil {
		return nil, fmt.Errorf("failed to parse message type: %w", err)
	}

	// Parse based on type
	switch mt.Type {
	case TypeRegister:
		var msg RegisterMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse register message: %w", err)
		}
		return &msg, nil

	case TypeRegistered:
		var msg RegisteredMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse registered message: %w", err)
		}
		return &msg, nil

	case TypeHeartbeat:
		var msg HeartbeatMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse heartbeat message: %w", err)
		}
		return &msg, nil

	case TypeHeartbeatAck:
		var msg HeartbeatAckMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse heartbeat_ack message: %w", err)
		}
		return &msg, nil

	case TypeError:
		var msg ErrorMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, fmt.Errorf("failed to parse error message: %w", err)
		}
		return &msg, nil

	default:
		return nil, fmt.Errorf("unknown message type: %s", mt.Type)
	}
}

// Close closes the underlying stream.
func (c *ControlStream) Close() error {
	return c.stream.Close()
}
