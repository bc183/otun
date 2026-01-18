# otun

A lightweight, open-source tunnel that exposes local services to the public internet. Think ngrok, but simple and self-hosted.

## Features

- **Reverse tunneling** - Expose localhost to the internet through NAT/firewalls
- **Stream multiplexing** - Multiple concurrent requests over a single TCP connection
- **Subdomain routing** - Multiple clients with unique subdomains
- **Automatic TLS** - Built-in Let's Encrypt certificate management
- **Single binary** - No external dependencies, just run it
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

**Or build from source:**

```bash
git clone https://github.com/bc183/otun.git
cd otun && make build
# Binary at ./bin/otun-client
```

### Connect to Public Server

```bash
./otun-client -server tunnel.otun.dev:4443 -local localhost:3000 -subdomain myapp
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

- Server with a public IP
- Domain with wildcard DNS (`*.tunnel.yourdomain.com` → your server IP)

### DNS Setup

Create these A records pointing to your server:

```
tunnel.yourdomain.com    →  A  →  your-server-ip
*.tunnel.yourdomain.com  →  A  →  your-server-ip
```

### Deploy

**Option 1: Direct binary**

```bash
# Download server binary
curl -LO https://github.com/bc183/otun/releases/latest/download/otun-server_linux_amd64.tar.gz
tar xzf otun-server_linux_amd64.tar.gz

# Run (requires root for ports 80/443)
sudo ./otun-server -domain tunnel.yourdomain.com
```

**Option 2: Docker**

```bash
docker run -d \
  --name otun \
  --restart unless-stopped \
  -p 4443:4443 -p 443:443 -p 80:80 \
  -v otun-certs:/var/lib/otun/certs \
  ghcr.io/bc183/otun:latest \
  otun-server -domain tunnel.yourdomain.com
```

**Option 3: Systemd service**

```bash
# Copy binary
sudo cp otun-server /usr/local/bin/

# Create systemd service
sudo tee /etc/systemd/system/otun.service << 'EOF'
[Unit]
Description=otun tunnel server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/otun-server -domain tunnel.yourdomain.com
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Enable and start
sudo systemctl enable otun
sudo systemctl start otun
```

### Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-domain` | (none) | Base domain for tunnels. Required for TLS. |
| `-control` | `:4443` | Port for tunnel client connections |
| `-https` | `:443` | Port for HTTPS traffic |
| `-http` | `:80` | Port for HTTP (ACME challenges + redirect) |
| `-certs` | `/var/lib/otun/certs` | Directory to store TLS certificates |
| `-debug` | `false` | Enable debug logging |

### Connect Clients

```bash
otun-client -server tunnel.yourdomain.com:4443 -local localhost:3000 -subdomain myapp
# Access at: https://myapp.tunnel.yourdomain.com
```

### Architecture

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   Browser   │────────►│ otun-server │────────►│ otun-client │
│             │  HTTPS  │  (TLS +     │  yamux  │             │
└─────────────┘         │   routing)  │         └─────────────┘
                        └─────────────┘                │
                                                       ▼
                                                ┌─────────────┐
                                                │   Local     │
                                                │   Service   │
                                                └─────────────┘
```

- **otun-server** - Handles TLS, routes requests by subdomain to connected clients
- **otun-client** - Forwards requests to your local service

## Development

```bash
# Build
make build

# Run tests
make test

# Run locally (HTTP-only mode, no TLS)
./bin/otun-server -http :8080 -control :4443
./bin/otun-client -server localhost:4443 -local localhost:3000 -subdomain test

# Test with curl
curl -H "Host: test.localhost:8080" http://localhost:8080/
```

## Project Structure

```
otun/
├── cmd/
│   ├── server/         # Server entry point
│   └── client/         # Client entry point
├── internal/
│   ├── server/         # Server implementation (TLS, routing)
│   ├── client/         # Client implementation
│   ├── protocol/       # Control protocol (register, heartbeat)
│   └── proxy/          # Bidirectional proxy helper
├── test/               # Integration tests
└── Dockerfile          # Server container
```

## Roadmap

- [x] Basic TCP tunnel
- [x] Connection multiplexing (yamux)
- [x] Control protocol (register, heartbeat)
- [x] HTTP routing by Host header
- [x] Native TLS with automatic Let's Encrypt
- [ ] Automatic reconnection
- [ ] Authentication
- [ ] Web UI for request inspection

## License

MIT License - see [LICENSE](LICENSE) for details.
