# otun

A lightweight, open-source tunnel that exposes local services to the public internet. Think ngrok, but simple and self-hosted.

## Features

- **Reverse tunneling** - Expose localhost to the internet through NAT/firewalls
- **Stream multiplexing** - Multiple concurrent requests over a single TCP connection
- **Control protocol** - JSON-based registration and heartbeat system
- **Zero dependencies on external services** - Self-host everything

## Quick Start

### Build

```bash
make build
```

### Run the Server

Start the tunnel server on a publicly accessible machine:

```bash
./bin/otun-server -control :4443 -public :8080
```

- `-control` - Port for tunnel clients to connect (default: `:4443`)
- `-public` - Port for public HTTP traffic (default: `:8080`)

### Run the Client

On your local machine, expose a local service:

```bash
./bin/otun-client -server your-server.com:4443 -local localhost:3000
```

- `-server` - Address of the tunnel server
- `-local` - Local service to expose (default: `localhost:8080`)

Your local service running on port 3000 is now accessible via `your-server.com:8080`.

## How It Works

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   Browser   │ ──────► │ otun-server │ ──────► │ otun-client │
│             │  HTTP   │  (public)   │  yamux  │             │
└─────────────┘         └─────────────┘         └─────────────┘
                                                       │
                                                       │ HTTP
                                                       ▼
                                                ┌─────────────┐
                                                │ Local       │
                                                │ Service     │
                                                └─────────────┘
```

1. **Client initiates connection** - The client connects to the server over TCP and establishes a [yamux](https://github.com/hashicorp/yamux) session for multiplexing
2. **Registration** - Client sends a register message, server responds with tunnel URL
3. **Traffic forwarding** - When the server receives public HTTP requests, it opens a new yamux stream to the client
4. **Local proxy** - Client forwards the stream to your local service and returns the response

## Development

```bash
# Build
make build

# Run tests
make test

# Clean
make clean
```

## Project Structure

```
otun/
├── cmd/
│   ├── server/         # Server entry point
│   └── client/         # Client entry point
├── internal/
│   ├── server/         # Server implementation
│   ├── client/         # Client implementation
│   ├── protocol/       # Control protocol (register, heartbeat)
│   └── proxy/          # Bidirectional proxy helper
└── test/               # Integration tests
```

## Roadmap

- [x] Phase 1: Basic TCP tunnel
- [x] Phase 2: Connection multiplexing (yamux)
- [x] Phase 3: Control protocol (register, heartbeat)
- [ ] Phase 4: HTTP-aware routing (Host header, multiple clients)
- [ ] Phase 5: TLS/HTTPS support
- [ ] Phase 6: Reconnection, CLI improvements, auth

## License

MIT License - see [LICENSE](LICENSE) for details.
