package client

import (
	"testing"
	"time"
)

func TestBackoff_NextDelay(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
		Jitter:       0, // Disable jitter for predictable tests
		MaxRetries:   0,
	}

	b := NewBackoff(config)

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 1 * time.Second},  // 1 * 2^0 = 1s
		{2, 2 * time.Second},  // 1 * 2^1 = 2s
		{3, 4 * time.Second},  // 1 * 2^2 = 4s
		{4, 8 * time.Second},  // 1 * 2^3 = 8s
		{5, 10 * time.Second}, // 1 * 2^4 = 16s, capped at 10s
		{6, 10 * time.Second}, // still capped
	}

	for _, tt := range tests {
		delay := b.NextDelay()
		if delay != tt.expected {
			t.Errorf("attempt %d: got %v, want %v", tt.attempt, delay, tt.expected)
		}
		if b.Attempt() != tt.attempt {
			t.Errorf("attempt count: got %d, want %d", b.Attempt(), tt.attempt)
		}
	}
}

func TestBackoff_Reset(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
		Jitter:       0,
		MaxRetries:   0,
	}

	b := NewBackoff(config)

	// Make some attempts
	b.NextDelay()
	b.NextDelay()
	b.NextDelay()

	if b.Attempt() != 3 {
		t.Errorf("before reset: got %d attempts, want 3", b.Attempt())
	}

	b.Reset()

	if b.Attempt() != 0 {
		t.Errorf("after reset: got %d attempts, want 0", b.Attempt())
	}

	// Next delay should be initial delay again
	delay := b.NextDelay()
	if delay != 1*time.Second {
		t.Errorf("after reset first delay: got %v, want 1s", delay)
	}
}

func TestBackoff_MaxRetriesReached(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
		Jitter:       0,
		MaxRetries:   3,
	}

	b := NewBackoff(config)

	// Should not be reached initially
	if b.MaxRetriesReached() {
		t.Error("should not be reached before any attempts")
	}

	b.NextDelay() // attempt 1
	if b.MaxRetriesReached() {
		t.Error("should not be reached after 1 attempt")
	}

	b.NextDelay() // attempt 2
	if b.MaxRetriesReached() {
		t.Error("should not be reached after 2 attempts")
	}

	b.NextDelay() // attempt 3
	if !b.MaxRetriesReached() {
		t.Error("should be reached after 3 attempts")
	}
}

func TestBackoff_UnlimitedRetries(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
		Jitter:       0,
		MaxRetries:   0, // unlimited
	}

	b := NewBackoff(config)

	// Should never be reached with unlimited retries
	for i := 0; i < 100; i++ {
		b.NextDelay()
		if b.MaxRetriesReached() {
			t.Errorf("should never be reached with unlimited retries, but reached at attempt %d", i+1)
		}
	}
}

func TestBackoff_Jitter(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.25, // 25% jitter
		MaxRetries:   0,
	}

	// Run multiple times to verify jitter produces variation
	seen := make(map[time.Duration]bool)
	for i := 0; i < 20; i++ {
		b := NewBackoff(config)
		delay := b.NextDelay()

		// With 25% jitter on 1s, delay should be between 0.75s and 1.25s
		if delay < 750*time.Millisecond || delay > 1250*time.Millisecond {
			t.Errorf("delay %v outside expected jitter range [750ms, 1250ms]", delay)
		}

		seen[delay] = true
	}

	// With jitter, we should see some variation (not all the same value)
	if len(seen) < 2 {
		t.Error("jitter should produce variation in delays")
	}
}

func TestDefaultBackoffConfig(t *testing.T) {
	config := DefaultBackoffConfig()

	if config.InitialDelay != 1*time.Second {
		t.Errorf("InitialDelay: got %v, want 1s", config.InitialDelay)
	}
	if config.MaxDelay != 60*time.Second {
		t.Errorf("MaxDelay: got %v, want 60s", config.MaxDelay)
	}
	if config.Multiplier != 2.0 {
		t.Errorf("Multiplier: got %v, want 2.0", config.Multiplier)
	}
	if config.Jitter != 0.25 {
		t.Errorf("Jitter: got %v, want 0.25", config.Jitter)
	}
	if config.MaxRetries != 0 {
		t.Errorf("MaxRetries: got %v, want 0", config.MaxRetries)
	}
}
