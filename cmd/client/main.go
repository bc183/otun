// Package main implements the otun client.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/bc183/otun/internal/client"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var (
	serverAddr string
	subdomain  string
	debug      bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "otun",
		Short: "Expose local services to the internet",
		Long:  `otun is a lightweight tunnel that exposes local services to the public internet.`,
	}

	httpCmd := &cobra.Command{
		Use:   "http <port> or http <host:port>",
		Short: "Expose a local HTTP service",
		Long: `Expose a local HTTP service to the internet.

Examples:
  otun http 3000                      # Expose localhost:3000
  otun http 8080 -s myapp             # Expose localhost:8080 with subdomain "myapp"
  otun http localhost:8080            # Expose localhost:8080
  otun http 192.168.1.10:3000         # Expose a service on your network`,
		Args: cobra.ExactArgs(1),
		Run:  runHTTP,
	}

	httpCmd.Flags().StringVarP(&serverAddr, "server", "S", "tunnel.otun.dev:4443", "Tunnel server address")
	httpCmd.Flags().StringVarP(&subdomain, "subdomain", "s", "", "Custom subdomain (random if not specified)")
	httpCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")

	rootCmd.AddCommand(httpCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runHTTP(cmd *cobra.Command, args []string) {
	// Setup logging
	if debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	// Parse the local address
	localAddr := args[0]
	if !strings.Contains(localAddr, ":") {
		// Just a port number, assume localhost
		localAddr = "localhost:" + localAddr
	}

	// Create and run client
	c := client.New(serverAddr, localAddr)
	if subdomain != "" {
		c = c.WithSubdomain(subdomain)
	}

	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
