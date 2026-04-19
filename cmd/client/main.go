// Package main implements the otun client.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bc183/otun/internal/client"
	"github.com/bc183/otun/internal/inspect"
	"github.com/bc183/otun/internal/version"
	"github.com/bc183/otun/internal/webui"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	configPath  string
	serverAddr  string
	subdomain   string
	token       string
	debug       bool
	noReconnect bool
	maxRetries  int

	inspectEnabled    bool
	noInspect         bool
	inspectAddr       string
	inspectStore      string
	inspectDB         string
	inspectMaxRecords int
	inspectMaxBody    int
)

// InspectConfig mirrors the `inspect:` section of the YAML config.
type InspectConfig struct {
	Enabled    *bool  `yaml:"enabled"`
	Addr       string `yaml:"addr"`
	Store      string `yaml:"store"`
	DB         string `yaml:"db"`
	MaxRecords *int   `yaml:"max_records"`
	MaxBody    *int   `yaml:"max_body"`
}

// Config represents the client configuration file.
type Config struct {
	Server     string         `yaml:"server"`
	Token      string         `yaml:"token"`
	Subdomain  string         `yaml:"subdomain"`
	Debug      *bool          `yaml:"debug"`
	Reconnect  *bool          `yaml:"reconnect"`
	MaxRetries *int           `yaml:"max_retries"`
	Inspect    *InspectConfig `yaml:"inspect"`
}

// loadConfig loads configuration from the config file.
// Returns nil if no config file exists.
func loadConfig(path string) (*Config, error) {
	// If no explicit path, use default ~/.otun.yaml
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil
		}
		path = filepath.Join(home, ".otun.yaml")
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}
	return &cfg, nil
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "otun",
		Short: "Expose local services to the internet",
		Long:  `otun is a lightweight tunnel that exposes local services to the public internet.`,
	}

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("otun " + version.Full())
		},
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

	httpCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file (default: ~/.otun.yaml)")
	httpCmd.Flags().StringVarP(&serverAddr, "server", "S", "tunnel.otun.dev:4443", "Tunnel server address")
	httpCmd.Flags().StringVarP(&subdomain, "subdomain", "s", "", "Custom subdomain (random if not specified)")
	httpCmd.Flags().StringVarP(&token, "token", "t", "", "API key for authentication")
	httpCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	httpCmd.Flags().BoolVar(&noReconnect, "no-reconnect", false, "Disable automatic reconnection")
	httpCmd.Flags().IntVar(&maxRetries, "max-retries", 0, "Maximum reconnection attempts (0 = unlimited)")

	httpCmd.Flags().BoolVar(&noInspect, "no-inspect", false, "Disable the local inspect UI")
	httpCmd.Flags().StringVar(&inspectAddr, "inspect-addr", "127.0.0.1:4040", "Address for the inspect UI")
	httpCmd.Flags().StringVar(&inspectStore, "inspect-store", "memory", "Inspect storage backend: memory | sqlite")
	httpCmd.Flags().StringVar(&inspectDB, "inspect-db", "", "Path to SQLite DB when --inspect-store=sqlite (default: ~/.otun/inspect.db)")
	httpCmd.Flags().IntVar(&inspectMaxRecords, "inspect-max-records", 500, "Maximum captured requests to keep in memory (memory store only; sqlite keeps everything)")
	httpCmd.Flags().IntVar(&inspectMaxBody, "inspect-max-body", 1<<20, "Maximum bytes of each request/response body to capture")

	rootCmd.AddCommand(httpCmd)
	rootCmd.AddCommand(versionCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runHTTP(cmd *cobra.Command, args []string) {
	// Load config file
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Apply config values where CLI flags weren't explicitly set
	if cfg != nil {
		if cfg.Server != "" && !cmd.Flags().Changed("server") {
			serverAddr = cfg.Server
		}
		if cfg.Token != "" && !cmd.Flags().Changed("token") {
			token = cfg.Token
		}
		if cfg.Subdomain != "" && !cmd.Flags().Changed("subdomain") {
			subdomain = cfg.Subdomain
		}
		if cfg.Debug != nil && !cmd.Flags().Changed("debug") {
			debug = *cfg.Debug
		}
		if cfg.Reconnect != nil && !cmd.Flags().Changed("no-reconnect") {
			noReconnect = !*cfg.Reconnect
		}
		if cfg.MaxRetries != nil && !cmd.Flags().Changed("max-retries") {
			maxRetries = *cfg.MaxRetries
		}
		if ic := cfg.Inspect; ic != nil {
			if ic.Enabled != nil && !cmd.Flags().Changed("no-inspect") {
				noInspect = !*ic.Enabled
			}
			if ic.Addr != "" && !cmd.Flags().Changed("inspect-addr") {
				inspectAddr = ic.Addr
			}
			if ic.Store != "" && !cmd.Flags().Changed("inspect-store") {
				inspectStore = ic.Store
			}
			if ic.DB != "" && !cmd.Flags().Changed("inspect-db") {
				inspectDB = ic.DB
			}
			if ic.MaxRecords != nil && !cmd.Flags().Changed("inspect-max-records") {
				inspectMaxRecords = *ic.MaxRecords
			}
			if ic.MaxBody != nil && !cmd.Flags().Changed("inspect-max-body") {
				inspectMaxBody = *ic.MaxBody
			}
		}
	}
	inspectEnabled = !noInspect

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

	// Create context that cancels on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Create and configure client
	c := client.New(serverAddr, localAddr).
		WithReconnect(!noReconnect).
		WithMaxRetries(maxRetries)

	if subdomain != "" {
		c = c.WithSubdomain(subdomain)
	}
	if token != "" {
		c = c.WithToken(token)
	}

	// Optional: inspect UI.
	if inspectEnabled {
		store, err := buildInspectStore()
		if err != nil {
			fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		opts := inspect.Options{
			MaxReqBody:  inspectMaxBody,
			MaxRespBody: inspectMaxBody,
		}
		c = c.WithInspector(store, opts)

		uiSrv, err := webui.New(webui.Config{
			Addr:  inspectAddr,
			Store: store,
			Replayer: &inspect.Replayer{
				Store:     store,
				LocalAddr: localAddr,
				Options:   opts,
			},
			TunnelURL: c.TunnelURL,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
			os.Exit(1)
		}
		go func() {
			if err := uiSrv.Run(ctx); err != nil && err != context.Canceled {
				log.Warn("inspect UI error", "error", err)
			}
		}()
	}

	// Run with reconnection support
	err = c.RunWithReconnect(ctx)

	if errors.Is(err, client.ErrShutdown) {
		log.Info("Shutting down...")
		return
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// buildInspectStore constructs the inspect.Store chosen by flags/config.
func buildInspectStore() (inspect.Store, error) {
	switch strings.ToLower(inspectStore) {
	case "", "memory", "mem":
		return inspect.NewMemoryStore(inspectMaxRecords), nil
	case "sqlite", "sqlite3":
		path := inspectDB
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve home dir: %w", err)
			}
			path = filepath.Join(home, ".otun", "inspect.db")
		}
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir: %w", err)
			}
		}
		return inspect.NewSQLiteStore(path)
	default:
		return nil, fmt.Errorf("unknown inspect store %q (want memory or sqlite)", inspectStore)
	}
}
