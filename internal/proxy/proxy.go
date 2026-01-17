// Package proxy provides utilities for bidirectional data transfer between connections.
package proxy

import (
	"errors"
	"io"
	"sync"
)

// halfCloser is implemented by connections that support closing the write side
// while keeping the read side open (TCP, TLS, yamux streams).
type halfCloser interface {
	CloseWrite() error
}

// Bidirectional copies data between two io.ReadWriteCloser connections.
// It blocks until both directions are done (either due to EOF or error).
// Both connections are closed when the function returns.
//
// When one direction completes (EOF), it calls CloseWrite on the destination
// to signal EOF to the other side, allowing graceful half-close semantics.
// This prevents abrupt connection termination and allows in-flight data to complete.
//
// Returns the first non-EOF error encountered, or nil if both directions
// completed successfully.
func Bidirectional(conn1, conn2 io.ReadWriteCloser) error {
	var wg sync.WaitGroup
	var err1, err2 error

	wg.Add(2)

	// conn1 -> conn2
	go func() {
		defer wg.Done()
		_, err1 = io.Copy(conn2, conn1)
		// Signal EOF to conn2's reader by closing write side
		closeWrite(conn2)
	}()

	// conn2 -> conn1
	go func() {
		defer wg.Done()
		_, err2 = io.Copy(conn1, conn2)
		// Signal EOF to conn1's reader by closing write side
		closeWrite(conn1)
	}()

	wg.Wait()

	// Close both connections fully
	conn1.Close()
	conn2.Close()

	// Return the first meaningful error
	return firstError(err1, err2)
}

// closeWrite attempts to half-close the write side of a connection.
func closeWrite(c io.ReadWriteCloser) {
	if hc, ok := c.(halfCloser); ok {
		hc.CloseWrite()
	}
}

// firstError returns the first non-nil, non-EOF error from the given errors.
// Returns nil if all errors are nil or EOF.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	return nil
}
