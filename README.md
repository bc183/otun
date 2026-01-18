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
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--subdomain` | `-s` | (random) | Custom subdomain |
| `--server` | `-S` | `tunnel.otun.dev:4443` | Tunnel server address |
| `--debug` | `-d` | `false` | Show debug logs |

## Features

- **Fast** - Single TCP connection with yamux multiplexing
- **Secure** - Automatic HTTPS with Let's Encrypt
- **Simple** - One command, no config files
- **Self-hostable** - Run your own server
- **WebSocket support** - Full bidirectional streaming

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
ExecStart=/usr/local/bin/otun-server -domain tunnel.example.com
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
- [ ] Automatic reconnection
- [ ] Authentication
- [ ] Web dashboard

## License

MIT
