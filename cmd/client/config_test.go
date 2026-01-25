package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_NoFile(t *testing.T) {
	// Non-existent file should return nil, nil
	cfg, err := loadConfig("/nonexistent/path/config.yaml")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config, got: %+v", cfg)
	}
}

func TestLoadConfig_ValidFile(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server: test.example.com:4443
token: secret-token
subdomain: myapp
debug: true
reconnect: false
max_retries: 5
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}

	if cfg.Server != "test.example.com:4443" {
		t.Errorf("expected server 'test.example.com:4443', got '%s'", cfg.Server)
	}
	if cfg.Token != "secret-token" {
		t.Errorf("expected token 'secret-token', got '%s'", cfg.Token)
	}
	if cfg.Subdomain != "myapp" {
		t.Errorf("expected subdomain 'myapp', got '%s'", cfg.Subdomain)
	}
	if cfg.Debug == nil || *cfg.Debug != true {
		t.Errorf("expected debug true, got %v", cfg.Debug)
	}
	if cfg.Reconnect == nil || *cfg.Reconnect != false {
		t.Errorf("expected reconnect false, got %v", cfg.Reconnect)
	}
	if cfg.MaxRetries == nil || *cfg.MaxRetries != 5 {
		t.Errorf("expected max_retries 5, got %v", cfg.MaxRetries)
	}
}

func TestLoadConfig_PartialFile(t *testing.T) {
	// Config with only some fields set
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server: partial.example.com:4443
token: partial-token
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}

	if cfg.Server != "partial.example.com:4443" {
		t.Errorf("expected server 'partial.example.com:4443', got '%s'", cfg.Server)
	}
	if cfg.Token != "partial-token" {
		t.Errorf("expected token 'partial-token', got '%s'", cfg.Token)
	}
	// Unset fields should be zero values
	if cfg.Subdomain != "" {
		t.Errorf("expected empty subdomain, got '%s'", cfg.Subdomain)
	}
	if cfg.Debug != nil {
		t.Errorf("expected nil debug, got %v", cfg.Debug)
	}
	if cfg.Reconnect != nil {
		t.Errorf("expected nil reconnect, got %v", cfg.Reconnect)
	}
	if cfg.MaxRetries != nil {
		t.Errorf("expected nil max_retries, got %v", cfg.MaxRetries)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
server: valid
token: [invalid yaml
  - not closed
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
	if cfg != nil {
		t.Errorf("expected nil config for invalid YAML, got: %+v", cfg)
	}
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty file should return empty config, not nil
	if cfg == nil {
		t.Fatal("expected empty config, got nil")
	}
}

func TestLoadConfig_DefaultPath(t *testing.T) {
	// When path is empty, should use ~/.otun.yaml
	// This test just verifies it doesn't crash when home dir exists
	cfg, err := loadConfig("")
	// Should either return nil (no file) or a config (if user has one)
	// Should not error unless file exists but is invalid
	if err != nil {
		t.Logf("Note: error loading default config (may be expected): %v", err)
	}
	_ = cfg // May or may not be nil depending on user's home dir
}

func TestLoadConfig_CommentsAndWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	content := `
# This is a comment
server: comment.example.com:4443  # inline comment

# Another comment
token: my-token

# Empty lines above and below

subdomain: test
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}

	if cfg.Server != "comment.example.com:4443" {
		t.Errorf("expected server 'comment.example.com:4443', got '%s'", cfg.Server)
	}
	if cfg.Token != "my-token" {
		t.Errorf("expected token 'my-token', got '%s'", cfg.Token)
	}
	if cfg.Subdomain != "test" {
		t.Errorf("expected subdomain 'test', got '%s'", cfg.Subdomain)
	}
}
