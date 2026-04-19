package inspect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bc183/otun/internal/proxy"
)

// Options configures the capture path.
type Options struct {
	// MaxReqBody caps how many request-body bytes are stored per record.
	// Bytes beyond the cap still flow to the local service.
	MaxReqBody int

	// MaxRespBody caps how many response-body bytes are stored per record.
	// Bytes beyond the cap still flow back to the tunnel server.
	MaxRespBody int

	// Subdomain is stamped on each record (informational).
	Subdomain string
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		MaxReqBody:  1 << 20, // 1 MiB
		MaxRespBody: 1 << 20,
	}
}

// Capture reads an HTTP request from stream, proxies it to localAddr,
// reads the response, proxies it back, and stores a Record in store.
//
// It implements the three-rule body policy:
//  1. Request with Upgrade → raw passthrough (headers captured, no bodies).
//  2. Response with text/event-stream → headers captured, body raw-copied.
//  3. Otherwise → bodies tee'd up to the caps; overflow flips to raw.
//
// This function is the per-stream equivalent of (*client.Client).handleStream
// with inspection enabled.
func Capture(ctx context.Context, stream net.Conn, localAddr string, store Store, opts Options) error {
	if opts.MaxReqBody <= 0 {
		opts.MaxReqBody = 1 << 20
	}
	if opts.MaxRespBody <= 0 {
		opts.MaxRespBody = 1 << 20
	}

	br := bufio.NewReader(stream)
	req, err := http.ReadRequest(br)
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}

	rec := &Record{
		ID:         NewID(),
		ReceivedAt: time.Now(),
		Subdomain:  opts.Subdomain,
		Host:       req.Host,
		Method:     req.Method,
		Path:       req.URL.RequestURI(),
		Proto:      req.Proto,
		ReqHeaders: cloneHeader(req.Header),
	}
	start := time.Now()

	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		rec.Err = fmt.Sprintf("dial local: %v", err)
		rec.DurationMs = time.Since(start).Milliseconds()
		_ = store.Add(ctx, rec)
		return err
	}
	defer localConn.Close()

	// Rule 1: Upgrade → raw passthrough after writing the handshake.
	if isUpgrade(req) {
		rec.ReqReason = BodyUpgrade
		rec.RespReason = BodyUpgrade
		if err := req.Write(localConn); err != nil {
			rec.Err = fmt.Sprintf("write upgrade: %v", err)
			rec.DurationMs = time.Since(start).Milliseconds()
			_ = store.Add(ctx, rec)
			return err
		}
		rec.DurationMs = time.Since(start).Milliseconds()
		_ = store.Add(ctx, rec)

		// The bufio reader may hold bytes beyond the headers (e.g. a framed
		// payload the client sent immediately). MultiReader keeps them first.
		combined := &streamWithBuffered{Conn: stream, r: io.MultiReader(br, stream)}
		return proxy.Bidirectional(combined, localConn)
	}

	// Rules 2 & 3 — parse request, capture bodies, relay.
	reqCap := newCapReader(req.Body, opts.MaxReqBody)
	req.Body = io.NopCloser(reqCap)

	if err := req.Write(localConn); err != nil {
		rec.Err = fmt.Sprintf("write local: %v", err)
		rec.ReqBody, rec.ReqSize, rec.ReqReason = reqCap.Snapshot()
		rec.DurationMs = time.Since(start).Milliseconds()
		_ = store.Add(ctx, rec)
		return err
	}
	rec.ReqBody, rec.ReqSize, rec.ReqReason = reqCap.Snapshot()

	resp, err := http.ReadResponse(bufio.NewReader(localConn), req)
	if err != nil {
		rec.Err = fmt.Sprintf("read response: %v", err)
		rec.DurationMs = time.Since(start).Milliseconds()
		_ = store.Add(ctx, rec)
		return err
	}
	defer resp.Body.Close()

	rec.Status = resp.StatusCode
	rec.RespHeaders = cloneHeader(resp.Header)

	// Rule 2: SSE → headers-only record, raw-stream the body back.
	if isSSE(resp) {
		rec.RespReason = BodyStreamed
		// resp.Write streams body as it arrives; chunked re-encoding
		// preserves per-Read event boundaries.
		err = resp.Write(stream)
		if err != nil && !errors.Is(err, io.EOF) {
			rec.Err = fmt.Sprintf("write response: %v", err)
		}
		rec.DurationMs = time.Since(start).Milliseconds()
		_ = store.Add(ctx, rec)
		return err
	}

	// Rule 3: tee the response body up to the cap.
	respCap := newCapReader(resp.Body, opts.MaxRespBody)
	resp.Body = io.NopCloser(respCap)
	if err := resp.Write(stream); err != nil && !errors.Is(err, io.EOF) {
		rec.Err = fmt.Sprintf("write response: %v", err)
	}
	rec.RespBody, rec.RespSize, rec.RespReason = respCap.Snapshot()
	rec.DurationMs = time.Since(start).Milliseconds()

	_ = store.Add(ctx, rec)
	return nil
}

// isUpgrade reports whether the request is an HTTP Upgrade (WebSocket etc.).
func isUpgrade(req *http.Request) bool {
	if req == nil {
		return false
	}
	if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return true
	}
	for _, v := range req.Header.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
				return true
			}
		}
	}
	return false
}

// isSSE reports whether the response is a Server-Sent Events stream.
func isSSE(resp *http.Response) bool {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "text/event-stream")
}

// cloneHeader copies headers defensively so later mutations don't bleed
// into stored records.
func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		copied := make([]string, len(vs))
		copy(copied, vs)
		out[k] = copied
	}
	return out
}

// streamWithBuffered adapts a bufio-wrapped stream back to net.Conn-ish
// semantics so it can be handed to proxy.Bidirectional.
type streamWithBuffered struct {
	net.Conn
	r io.Reader
}

func (s *streamWithBuffered) Read(b []byte) (int, error) { return s.r.Read(b) }
