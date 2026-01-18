// Package main implements the otun server.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/bc183/otun/internal/server"
)

func main() {
	controlAddr := flag.String("control", ":4443", "Control port address for tunnel client connections")
	httpsAddr := flag.String("https", ":443", "HTTPS port address for public traffic")
	httpAddr := flag.String("http", ":80", "HTTP port address for ACME challenges (and HTTP-only mode)")
	domain := flag.String("domain", "", "Base domain for tunnels (e.g., tunnel.example.com). If empty, runs in HTTP-only mode.")
	certDir := flag.String("certs", "/var/lib/otun/certs", "Directory to store TLS certificates")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Setup logging
	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	// Create and run server
	srv := server.New(*controlAddr, *httpsAddr, *httpAddr, *domain, *certDir)
	if err := srv.Run(); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
