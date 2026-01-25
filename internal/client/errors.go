package client

import (
	"errors"
	"net"
	"syscall"
)

// Sentinel errors for client operations.
var (
	// ErrShutdown indicates the client was shut down intentionally (e.g., via context cancellation).
	ErrShutdown = errors.New("client shutdown")

	// ErrPermanentFailure indicates an error that should not trigger reconnection.
	ErrPermanentFailure = errors.New("permanent failure")

	// ErrSubdomainTaken indicates the requested subdomain is already in use.
	ErrSubdomainTaken = errors.New("subdomain already in use")

	// ErrMaxRetriesExceeded indicates the maximum number of reconnection attempts was reached.
	ErrMaxRetriesExceeded = errors.New("maximum reconnection attempts exceeded")
)

// isPermanentError returns true if the error should not trigger a reconnection attempt.
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}

	// Check for our sentinel errors
	if errors.Is(err, ErrShutdown) ||
		errors.Is(err, ErrPermanentFailure) ||
		errors.Is(err, ErrSubdomainTaken) ||
		errors.Is(err, ErrMaxRetriesExceeded) {
		return true
	}

	return false
}

// isTransientError returns true if the error is a known transient network error.
// Returns false for unknown errors - caller should decide whether to reconnect.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	if isPermanentError(err) {
		return false
	}

	// Check for network errors with Timeout/Temporary methods
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	// Check for specific syscall errors that indicate transient failures
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}

	return false
}
