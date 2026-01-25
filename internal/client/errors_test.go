package client

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestIsPermanentError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"ErrShutdown", ErrShutdown, true},
		{"ErrPermanentFailure", ErrPermanentFailure, true},
		{"ErrSubdomainTaken", ErrSubdomainTaken, true},
		{"ErrMaxRetriesExceeded", ErrMaxRetriesExceeded, true},
		{"wrapped ErrShutdown", fmt.Errorf("outer: %w", ErrShutdown), true},
		{"wrapped ErrSubdomainTaken", fmt.Errorf("outer: %w", ErrSubdomainTaken), true},
		{"generic error", errors.New("some error"), false},
		{"connection refused", syscall.ECONNREFUSED, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPermanentError(tt.err)
			if result != tt.expected {
				t.Errorf("isPermanentError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"ErrShutdown (permanent)", ErrShutdown, false},
		{"ErrSubdomainTaken (permanent)", ErrSubdomainTaken, false},
		// Raw syscall errors don't implement net.Error, so they return false
		// In real network operations, these are wrapped in net.OpError
		{"raw ECONNREFUSED", syscall.ECONNREFUSED, false},
		{"generic error", errors.New("unknown error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTransientError(tt.err)
			if result != tt.expected {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// mockNetError implements net.Error for testing
type mockNetError struct {
	timeout   bool
	temporary bool
}

func (e *mockNetError) Error() string   { return "mock net error" }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return e.temporary }

// Verify mockNetError implements net.Error
var _ net.Error = (*mockNetError)(nil)

func TestIsTransientError_NetError(t *testing.T) {
	tests := []struct {
		name     string
		err      net.Error
		expected bool
	}{
		{"timeout error", &mockNetError{timeout: true, temporary: false}, true},
		{"temporary error", &mockNetError{timeout: false, temporary: true}, true},
		{"timeout and temporary", &mockNetError{timeout: true, temporary: true}, true},
		{"neither timeout nor temporary", &mockNetError{timeout: false, temporary: false}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTransientError(tt.err)
			if result != tt.expected {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestIsTransientError_WrappedNetError(t *testing.T) {
	netErr := &mockNetError{timeout: true}
	wrapped := fmt.Errorf("connection failed: %w", netErr)

	if !isTransientError(wrapped) {
		t.Error("wrapped timeout net.Error should be transient")
	}
}

func TestIsTransientError_NetOpError(t *testing.T) {
	// This is how real network errors appear - wrapped in net.OpError
	opErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.ECONNREFUSED,
	}

	// net.OpError implements net.Error, but Temporary() returns false for ECONNREFUSED
	// and Timeout() also returns false, so this is not considered transient
	// This is the expected behavior - we only retry on timeout/temporary errors
	result := isTransientError(opErr)
	// ECONNREFUSED is not a timeout or temporary error per net.Error interface
	if result != false {
		t.Errorf("ECONNREFUSED OpError should not be transient (not timeout/temporary), got %v", result)
	}

	// Test with a timeout error
	timeoutOpErr := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: &mockNetError{timeout: true},
	}
	if !isTransientError(timeoutOpErr) {
		t.Error("timeout OpError should be transient")
	}
}
