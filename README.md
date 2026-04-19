# otun (Open Tunnel)

A lightweight, open-source ngrok alternative. Expose local services to the internet in seconds.

```bash
otun http 3000
```
```
INFO Tunnel ready! url=https://af2c9b1e.tunnel.otun.dev
INFO Forwarding requests to=localhost:3000
INFO Request method=GET path=/
```

## Installation

**Quick install (macOS/Linux):**
```bash
curl -fsSL https://raw.githubusercontent.com/bc183/otun/main/install.sh | sh
```

**Manual download:** [GitHub Releases](https://github.com/bc183/otun/releases/latest)

**From source:**
```bash
git clone https://github.com/bc183/otun && cd otun && make build
# Binary at ./bin/otun
```

## Usage

```bash
otun http 3000                    # Expose localhost:3000
otun http 3000 -s myapp           # Custom subdomain → https://myapp.tunnel.otun.dev
otun http 8080 -S myserver:4443   # Use your own server
otun http 3000 -t my-api-key      # Authenticate with API key
otun version                      # Show version info
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--subdomain` | `-s` | (random) | Custom subdomain |
| `--server` | `-S` | `tunnel.otun.dev:4443` | Tunnel server address |
| `--token` | `-t` | | API key for authentication |
| `--config` | `-c` | `~/.otun.yaml` | Path to config file |
| `--debug` | `-d` | `false` | Show debug logs |
| `--no-reconnect` | | `false` | Disable automatic reconnection |
| `--max-retries` | | `0` | Max reconnection attempts (0 = unlimited) |
| `--no-inspect` | | `false` | Disable the local inspect UI |
| `--inspect-addr` | | `127.0.0.1:4040` | Address for the inspect UI |
| `--inspect-store` | | `memory` | Storage backend: `memory` or `sqlite` |
| `--inspect-db` | | `~/.otun/inspect.db` | SQLite path (when `--inspect-store=sqlite`) |
| `--inspect-max-records` | | `500` | Rolling-buffer size (memory store only; sqlite persists everything) |
| `--inspect-max-body` | | `1048576` | Max bytes of each body to capture (1 MiB) |

## Config File

Store settings in `~/.otun.yaml` to avoid repeating flags:

```yaml
server: tunnel.example.com:4443
token: my-api-key
subdomain: myapp
debug: false
reconnect: true
max_retries: 0

inspect:
  enabled: true
  addr: 127.0.0.1:4040
  store: memory          # or sqlite
  db: ~/.otun/inspect.db
  max_records: 500       # memory store only
  max_body: 1048576
```

CLI flags override config file values.

## Request Inspector

When you run `otun http`, a local inspection UI is served at
[http://127.0.0.1:4040](http://127.0.0.1:4040). Every request flowing
through the tunnel is captured with its headers, body, status, and
duration — filter by method/status/path, open a request to see the full
request and response, and click **↻ replay** to re-fire it directly against
your local service.

**In-memory (default):** rolling buffer of the last
`--inspect-max-records` requests (500 by default). Fast, zero-config,
discarded on exit.

**SQLite (persistent):** survives restarts and keeps every request.

```bash
otun http 3000 --inspect-store=sqlite
# records written to ~/.otun/inspect.db by default; override with --inspect-db
```

Request and response bodies are captured up to `--inspect-max-body` bytes
(1 MiB default); larger bodies still pass through to your service
unchanged, the UI just shows the prefix. WebSocket upgrades and
`text/event-stream` responses bypass capture and stream through raw.

Disable the inspector with `--no-inspect` or move it off the default port
with `--inspect-addr=127.0.0.1:9999`.

## Features

- **Fast** - Single TCP connection with yamux multiplexing
- **Secure** - Automatic HTTPS with Let's Encrypt
- **Reliable** - Auto-reconnects on connection loss with exponential backoff
- **Authenticated** - Optional API key authentication
- **Simple** - One command, optional config file
- **Self-hostable** - Run your own server
- **WebSocket support** - Full bidirectional streaming
- **Request inspector** - Local UI to view, filter, and replay captured traffic

## Self-Hosting

Run your own tunnel server with automatic TLS.

### 1. Setup DNS

Point your domain to your server:
```
tunnel.example.com    →  A  →  your-server-ip
*.tunnel.example.com  →  A  →  your-server-ip
```

### 2. Run Server

**Binary:**
```bash
curl -LO https://github.com/bc183/otun/releases/latest/download/otun-server_linux_amd64.tar.gz
tar xzf otun-server_linux_amd64.tar.gz
sudo ./otun-server -domain tunnel.example.com
```

**Docker:**
```bash
docker run -d --name otun --restart unless-stopped \
  -p 4443:4443 -p 443:443 -p 80:80 \
  -v otun-certs:/var/lib/otun/certs \
  ghcr.io/bc183/otun:latest \
  otun-server -domain tunnel.example.com
```

**Systemd:**
```bash
sudo tee /etc/systemd/system/otun.service << 'EOF'
[Unit]
Description=otun tunnel server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/otun-server -domain tunnel.example.com -api-keys "your-secret-key"
Restart=always

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable --now otun
```

### 3. Connect

```bash
otun http 3000 -S tunnel.example.com:4443
```

### Server Options

| Flag | Default | Description |
|------|---------|-------------|
| `-domain` | (required) | Base domain for tunnels |
| `-control` | `:4443` | Client connection port |
| `-https` | `:443` | Public HTTPS port |
| `-http` | `:80` | ACME challenge port |
| `-certs` | `/var/lib/otun/certs` | Certificate storage |
| `-api-keys` | | Comma-separated API keys (enables auth) |
| `-version` | | Print version and exit |

### Authentication

To require API keys for connections:

```bash
# Server
otun-server -domain tunnel.example.com -api-keys "key1,key2,key3"

# Client
otun http 3000 -t key1
```

When `-api-keys` is set, clients must provide a valid token to connect.

## How It Works

```
┌──────────┐        ┌─────────────┐        ┌──────┐        ┌─────────┐
│ Browser  │──HTTPS─▶│ otun-server │──yamux─▶│ otun │──HTTP─▶│ Local   │
└──────────┘        └─────────────┘        └──────┘        │ Service │
                                                           └─────────┘
```

1. Client (`otun`) connects to server over TCP with yamux multiplexing
2. Server terminates TLS and routes requests by subdomain
3. Requests are forwarded through the tunnel to your local service

## Development

```bash
make build    # Build binaries
make test     # Run tests

# Local testing (no TLS)
./bin/otun-server -http :8080 -control :4443
./bin/otun http 3000 -s test -S localhost:4443
curl -H "Host: test.localhost:8080" http://localhost:8080/
```

## Roadmap

- [x] Stream multiplexing (yamux)
- [x] Subdomain routing
- [x] Automatic TLS (Let's Encrypt)
- [x] Request logging
- [x] Automatic reconnection
- [x] API key authentication
- [x] Config file support
- [x] Web dashboard (request inspection UI)

## License

MIT
