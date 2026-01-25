package client

import (
	"math"
	"math/rand"
	"time"
)

// BackoffConfig configures the exponential backoff behavior.
type BackoffConfig struct {
	// InitialDelay is the starting delay (default: 1s)
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries (default: 60s)
	MaxDelay time.Duration

	// Multiplier is the factor to increase delay by (default: 2.0)
	Multiplier float64

	// Jitter adds randomness to delays (0.0-1.0, default: 0.25 = 25%)
	Jitter float64

	// MaxRetries is the maximum number of retries (0 = unlimited)
	MaxRetries int
}

// DefaultBackoffConfig returns sensible defaults for reconnection.
func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.25,
		MaxRetries:   0, // unlimited
	}
}

// Backoff tracks retry state and calculates delays.
type Backoff struct {
	config   BackoffConfig
	attempt  int
	rng      *rand.Rand
}

// NewBackoff creates a new Backoff with the given configuration.
func NewBackoff(config BackoffConfig) *Backoff {
	return &Backoff{
		config:  config,
		attempt: 0,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NextDelay returns the delay for the next retry attempt.
// Call this after a failed attempt to get the delay before retrying.
func (b *Backoff) NextDelay() time.Duration {
	b.attempt++

	// Calculate base delay with exponential backoff
	delay := float64(b.config.InitialDelay) * math.Pow(b.config.Multiplier, float64(b.attempt-1))

	// Cap at max delay
	if delay > float64(b.config.MaxDelay) {
		delay = float64(b.config.MaxDelay)
	}

	// Apply jitter: delay * (1 + random(-jitter, +jitter))
	if b.config.Jitter > 0 {
		jitterRange := delay * b.config.Jitter
		jitterValue := (b.rng.Float64()*2 - 1) * jitterRange // range: -jitter to +jitter
		delay += jitterValue
	}

	// Ensure delay is not negative
	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// Reset resets the backoff state after a successful connection.
func (b *Backoff) Reset() {
	b.attempt = 0
}

// Attempt returns the current attempt number (0 = no attempts yet).
func (b *Backoff) Attempt() int {
	return b.attempt
}

// MaxRetriesReached returns true if max retries has been exceeded.
// Always returns false if MaxRetries is 0 (unlimited).
func (b *Backoff) MaxRetriesReached() bool {
	if b.config.MaxRetries == 0 {
		return false
	}
	return b.attempt >= b.config.MaxRetries
}
