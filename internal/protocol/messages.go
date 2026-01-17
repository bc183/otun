// Package protocol defines the control protocol messages for otun.
package protocol

// Message types for the control protocol.
const (
	TypeRegister     = "register"
	TypeRegistered   = "registered"
	TypeHeartbeat    = "heartbeat"
	TypeHeartbeatAck = "heartbeat_ack"
	TypeError        = "error"
)

// RegisterMessage is sent by the client to request a tunnel.
type RegisterMessage struct {
	Type      string `json:"type"` // always "register"
	Subdomain string `json:"subdomain,omitempty"`
}

// RegisteredMessage is sent by the server to confirm tunnel registration.
type RegisteredMessage struct {
	Type      string `json:"type"` // always "registered"
	URL       string `json:"url"`
	Subdomain string `json:"subdomain"`
}

// HeartbeatMessage is sent by the client as a keepalive ping.
type HeartbeatMessage struct {
	Type string `json:"type"` // always "heartbeat"
}

// HeartbeatAckMessage is sent by the server as a keepalive pong.
type HeartbeatAckMessage struct {
	Type string `json:"type"` // always "heartbeat_ack"
}

// ErrorMessage is sent in either direction to report an error.
type ErrorMessage struct {
	Type    string `json:"type"` // always "error"
	Message string `json:"message"`
}

// NewRegisterMessage creates a register message.
func NewRegisterMessage(subdomain string) *RegisterMessage {
	return &RegisterMessage{
		Type:      TypeRegister,
		Subdomain: subdomain,
	}
}

// NewRegisteredMessage creates a registered message.
func NewRegisteredMessage(url, subdomain string) *RegisteredMessage {
	return &RegisteredMessage{
		Type:      TypeRegistered,
		URL:       url,
		Subdomain: subdomain,
	}
}

// NewHeartbeatMessage creates a heartbeat message.
func NewHeartbeatMessage() *HeartbeatMessage {
	return &HeartbeatMessage{
		Type: TypeHeartbeat,
	}
}

// NewHeartbeatAckMessage creates a heartbeat acknowledgment message.
func NewHeartbeatAckMessage() *HeartbeatAckMessage {
	return &HeartbeatAckMessage{
		Type: TypeHeartbeatAck,
	}
}

// NewErrorMessage creates an error message.
func NewErrorMessage(message string) *ErrorMessage {
	return &ErrorMessage{
		Type:    TypeError,
		Message: message,
	}
}
