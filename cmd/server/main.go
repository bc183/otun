// Package main implements the otun server.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/bc183/otun/internal/server"
	"github.com/bc183/otun/internal/version"
)

func main() {
	controlAddr := flag.String("control", ":4443", "Control port address for tunnel client connections")
	httpsAddr := flag.String("https", ":443", "HTTPS port address for public traffic")
	httpAddr := flag.String("http", ":80", "HTTP port address for ACME challenges (and HTTP-only mode)")
	domain := flag.String("domain", "", "Base domain for tunnels (e.g., tunnel.example.com). If empty, runs in HTTP-only mode.")
	certDir := flag.String("certs", "/var/lib/otun/certs", "Directory to store TLS certificates")
	apiKeys := flag.String("api-keys", "", "Comma-separated list of valid API keys (if set, authentication is required)")
	debug := flag.Bool("debug", false, "Enable debug logging")
	showVersion := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("otun-server " + version.Full())
		os.Exit(0)
	}

	// Setup logging
	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	// Parse API keys
	var keys []string
	if *apiKeys != "" {
		keys = strings.Split(*apiKeys, ",")
		slog.Info("API key authentication enabled", "key_count", len(keys))
	}

	// Create and run server
	srv := server.New(*controlAddr, *httpsAddr, *httpAddr, *domain, *certDir, keys)
	if err := srv.Run(); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
