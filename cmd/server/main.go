// Package main implements the otun server.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/bc183/otun/internal/server"
)

func main() {
	controlAddr := flag.String("control", ":4443", "Control port address for client connections")
	publicAddr := flag.String("public", ":8080", "Public port address for incoming traffic")
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
	srv := server.New(*controlAddr, *publicAddr)
	if err := srv.Run(); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
