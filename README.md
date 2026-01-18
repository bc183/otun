# otun

A lightweight, open-source tunnel that exposes local services to the public internet. Think ngrok, but simple and self-hosted.

## Features

- **Reverse tunneling** - Expose localhost to the internet through NAT/firewalls
- **Stream multiplexing** - Multiple concurrent requests over a single TCP connection
- **Subdomain routing** - Multiple clients with unique subdomains
- **Control protocol** - JSON-based registration and heartbeat system
- **Zero dependencies on external services** - Self-host everything

## Quick Start

### Install Client

**Download binary (Linux/macOS/Windows):**

```bash
# Linux (amd64)
curl -LO https://github.com/bc183/otun/releases/latest/download/otun-client_linux_amd64.tar.gz
tar xzf otun-client_linux_amd64.tar.gz

# macOS (Apple Silicon)
curl -LO https://github.com/bc183/otun/releases/latest/download/otun-client_darwin_arm64.tar.gz
tar xzf otun-client_darwin_arm64.tar.gz

# macOS (Intel)
curl -LO https://github.com/bc183/otun/releases/latest/download/otun-client_darwin_amd64.tar.gz
tar xzf otun-client_darwin_amd64.tar.gz
```

**Or with Go:**

```bash
go install github.com/bc183/otun/cmd/client@latest
```

**Or build from source:**

```bash
git clone https://github.com/bc183/otun.git
cd otun && make build
# Binary at ./bin/otun-client
```

### Connect to Public Server

```bash
./bin/otun-client -server tunnel.otun.dev:4443 -local localhost:3000 -subdomain myapp
```

Your local service is now accessible at `https://myapp.tunnel.otun.dev`

### Client Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-server` | `localhost:4443` | Tunnel server address |
| `-local` | `localhost:3000` | Local service to expose |
| `-subdomain` | (random) | Preferred subdomain |
| `-debug` | `false` | Enable debug logging |

## Self-Hosting

### Prerequisites

- Server with Docker and Docker Compose
- Domain with wildcard DNS (`*.tunnel.yourdomain.com` → your server IP)

### Deploy with Docker Compose

1. Download config files:

```bash
mkdir otun && cd otun
curl -LO https://raw.githubusercontent.com/bc183/otun/main/docker-compose.yml
curl -LO https://raw.githubusercontent.com/bc183/otun/main/Caddyfile

# Edit Caddyfile with your domain
sed -i 's/tunnel.otun.dev/tunnel.yourdomain.com/g' Caddyfile
```

2. Start services:

```bash
docker compose up -d
```

3. Connect clients:

```bash
otun-client -server yourdomain.com:4443 -local localhost:3000 -subdomain myapp
# Access at: https://myapp.tunnel.yourdomain.com
```

### DNS Setup

Create a wildcard A record pointing to your server:

```
*.tunnel.yourdomain.com  →  A  →  your-server-ip
```

### Architecture

```
┌─────────────┐      ┌───────┐      ┌─────────────┐      ┌─────────────┐
│   Browser   │─────►│ Caddy │─────►│ otun-server │─────►│ otun-client │
│             │ HTTPS│ (TLS) │ HTTP │             │ yamux│             │
└─────────────┘      └───────┘      └─────────────┘      └─────────────┘
                                                                │
                                                                ▼
                                                         ┌─────────────┐
                                                         │   Local     │
                                                         │   Service   │
                                                         └─────────────┘
```

- **Caddy** - Handles TLS termination with automatic HTTPS
- **otun-server** - Routes requests by subdomain to connected clients
- **otun-client** - Forwards requests to your local service

## Development

```bash
# Build
make build

# Run tests
make test

# Run locally (no TLS)
./bin/otun-server -control :4443 -public :8080
./bin/otun-client -server localhost:4443 -local localhost:3000 -subdomain test

# Test with curl
curl -H "Host: test.tunnel.localhost" http://localhost:8080/
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
├── test/               # Integration tests
├── Dockerfile          # Server container
├── docker-compose.yml  # Full deployment stack
└── Caddyfile           # Caddy reverse proxy config
```

## Roadmap

- [x] Basic TCP tunnel
- [x] Connection multiplexing (yamux)
- [x] Control protocol (register, heartbeat)
- [x] HTTP routing by Host header
- [x] TLS/HTTPS (via Caddy)
- [ ] Automatic reconnection
- [ ] Authentication
- [ ] Web UI for request inspection

## License

MIT License - see [LICENSE](LICENSE) for details.
