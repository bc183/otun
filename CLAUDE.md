# CLAUDE.md - Project Guide for otun

## Project Overview

**otun** (Open Tunnel) is a lightweight, open-source ngrok alternative written in Go. It exposes local services to the public internet via reverse tunneling.

## Architecture Decisions

### Control Stream (Option A - Single Connection)
```
Client connects to server (single TCP connection)
    └── yamux session wraps it
        ├── Stream 0: Control (JSON messages - register, heartbeat, errors)
        ├── Stream 1: HTTP request #1
        ├── Stream 2: HTTP request #2
        └── ...
```
- Single TCP connection handles both control and data
- First yamux stream (Stream 0) is dedicated to control messages
- Server opens new streams for each incoming HTTP request

### Configuration Priority
CLI flags > Environment variables > Config file > Defaults

### Subdomain Handling
- Random 8-character alphanumeric generation if not specified
- Reject with error if requested subdomain is already in use (like username validation)

## Technical Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| License | MIT | Maximum community adoption |
| Go version | 1.25.1+ | Use system version, modern stdlib features |
| HTTP framework | stdlib `net/http` | Transparent proxy needs raw access, WebSocket hijacking support |
| Multiplexing | `github.com/hashicorp/yamux` | Battle-tested, used by Consul/Nomad |
| CLI framework | `github.com/spf13/cobra` | Phase 6 only |
| Database | None (Phases 1-5) | All state is ephemeral; SQLite if needed in Phase 6 |
| Logging | stdlib `log/slog` | Structured logging, no dependencies |
| Error handling | Wrapped with context | `fmt.Errorf("operation failed: %w", err)` |
| Tests | Written alongside implementation | Priority, not afterthought |

## Project Structure

```
otun/
├── cmd/
│   ├── server/
│   │   └── main.go
│   └── client/
│       └── main.go
├── internal/
│   ├── server/
│   │   ├── server.go        # Main server logic
│   │   ├── registry.go      # Tunnel registry (subdomain -> session)
│   │   └── http_handler.go  # HTTP routing by Host header
│   ├── client/
│   │   └── client.go        # Main client logic
│   ├── protocol/
│   │   ├── messages.go      # Control message types
│   │   └── control.go       # Control stream handling
│   └── proxy/
│       └── proxy.go         # Bidirectional copy helper
├── go.mod
├── go.sum
├── Makefile
├── LICENSE
└── README.md
```

## Implementation Phases

### Phase 1: Basic TCP Tunnel (No Multiplexing)
- Simple bidirectional byte forwarding
- Single client support
- Files: `internal/proxy/proxy.go`, `cmd/server/main.go`, `cmd/client/main.go`

### Phase 2: Connection Multiplexing
- Add yamux for stream multiplexing
- Support multiple concurrent requests over single connection

### Phase 3: Control Protocol
- JSON-based control messages over Stream 0
- Message types: register, registered, heartbeat, error
- Heartbeat: client sends every 30s, server times out after 90s

### Phase 4: HTTP-Aware Routing
- Route by Host header to correct tunnel
- Support multiple simultaneous clients with different subdomains
- TunnelRegistry with sync.RWMutex for thread safety

### Phase 5: TLS & HTTPS
- TLS termination at server
- Manual certs or autocert for Let's Encrypt
- Forward plain HTTP through tunnel to client

### Phase 6: Polish & Features
- Reconnection with exponential backoff
- cobra CLI
- Optional: request inspection UI, authentication, rate limiting

## Code Style Guidelines

### Error Handling
```go
// Good - wrapped with context
if err != nil {
    return fmt.Errorf("failed to connect to %s: %w", addr, err)
}

// Bad - no context
if err != nil {
    return err
}
```

### Logging
```go
import "log/slog"

slog.Info("tunnel registered", "subdomain", subdomain, "remote_addr", conn.RemoteAddr())
slog.Error("connection failed", "error", err)
```

### Testing
```go
// Table-driven tests preferred
func TestProxy(t *testing.T) {
    tests := []struct {
        name    string
        input   []byte
        want    []byte
        wantErr bool
    }{
        // ...
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

## Dependencies

Minimal, prefer stdlib:
```
github.com/hashicorp/yamux  # Stream multiplexing (required)
github.com/spf13/cobra      # CLI framework (Phase 6 only)
```

## Build Commands

```bash
make build      # Build both binaries
make test       # Run all tests
make lint       # Run linter
make clean      # Clean build artifacts
```

## Protocol Reference

### Control Messages (JSON over Stream 0)

```go
type ControlMessage struct {
    Type    string          `json:"type"`
    Payload json.RawMessage `json:"payload"`
}
```

| Type | Direction | Payload | Description |
|------|-----------|---------|-------------|
| `register` | client→server | `{"subdomain": "optional"}` | Request tunnel |
| `registered` | server→client | `{"url": "https://abc123.tunnel.dev", "subdomain": "abc123"}` | Confirm tunnel |
| `heartbeat` | client→server | `{}` | Keepalive ping |
| `heartbeat_ack` | server→client | `{}` | Keepalive pong |
| `error` | bidirectional | `{"message": "..."}` | Error notification |

## Future Considerations

- TCP/SSH tunnels (not just HTTP)
- WebSocket passthrough (supported via http.Hijacker)
- SQLite for persistent subdomain reservations
- Web UI for request inspection
- API keys and rate limiting
