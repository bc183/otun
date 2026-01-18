// Package main implements the otun client.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/bc183/otun/internal/client"
)

func main() {
	serverAddr := flag.String("server", "localhost:4443", "Server control port address")
	localAddr := flag.String("local", "localhost:3000", "Local service address to forward traffic to")
	subdomain := flag.String("subdomain", "", "Preferred subdomain (random if not specified)")
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

	// Create and run client
	c := client.New(*serverAddr, *localAddr)
	if *subdomain != "" {
		c = c.WithSubdomain(*subdomain)
	}
	if err := c.Run(); err != nil {
		slog.Error("client error", "error", err)
		os.Exit(1)
	}
}
