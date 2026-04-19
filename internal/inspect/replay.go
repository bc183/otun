package inspect

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Replayer holds the context a Replay needs: where to send the replayed
// request and which Store to log the result in.
type Replayer struct {
	Store     Store
	LocalAddr string
	Options   Options
}

// Replay fires the captured request in r against the configured localAddr
// and stores the result as a new Record with Replay=true, OfID=r.ID.
// Returns the new record.
func (rp *Replayer) Replay(ctx context.Context, id string) (*Record, error) {
	orig, err := rp.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if orig.Replay {
		// Allow replay-of-replay, but anchor back to the original target.
		if orig.OfID != "" {
			id = orig.OfID
		}
	}

	opts := rp.Options
	if opts.MaxReqBody <= 0 {
		opts.MaxReqBody = 1 << 20
	}
	if opts.MaxRespBody <= 0 {
		opts.MaxRespBody = 1 << 20
	}

	newRec := &Record{
		ID:         NewID(),
		ReceivedAt: time.Now(),
		Subdomain:  orig.Subdomain,
		Host:       orig.Host,
		Method:     orig.Method,
		Path:       orig.Path,
		Proto:      orig.Proto,
		ReqHeaders: cloneHeader(orig.ReqHeaders),
		ReqBody:    append([]byte(nil), orig.ReqBody...),
		ReqSize:    int64(len(orig.ReqBody)),
		Replay:     true,
		OfID:       id,
	}
	start := time.Now()

	// Rebuild the request from the captured bytes.
	req, err := rebuildRequest(orig)
	if err != nil {
		newRec.Err = fmt.Sprintf("rebuild request: %v", err)
		newRec.DurationMs = time.Since(start).Milliseconds()
		_ = rp.Store.Add(ctx, newRec)
		return newRec, err
	}

	localConn, err := net.Dial("tcp", rp.LocalAddr)
	if err != nil {
		newRec.Err = fmt.Sprintf("dial local: %v", err)
		newRec.DurationMs = time.Since(start).Milliseconds()
		_ = rp.Store.Add(ctx, newRec)
		return newRec, err
	}
	defer localConn.Close()

	if err := req.Write(localConn); err != nil {
		newRec.Err = fmt.Sprintf("write request: %v", err)
		newRec.DurationMs = time.Since(start).Milliseconds()
		_ = rp.Store.Add(ctx, newRec)
		return newRec, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(localConn), req)
	if err != nil {
		newRec.Err = fmt.Sprintf("read response: %v", err)
		newRec.DurationMs = time.Since(start).Milliseconds()
		_ = rp.Store.Add(ctx, newRec)
		return newRec, err
	}
	defer resp.Body.Close()

	newRec.Status = resp.StatusCode
	newRec.RespHeaders = cloneHeader(resp.Header)

	body, err := readCapped(resp.Body, opts.MaxRespBody)
	if err != nil && err != io.EOF {
		newRec.Err = fmt.Sprintf("read response body: %v", err)
	}
	newRec.RespBody = body.captured
	newRec.RespSize = body.total
	newRec.RespReason = body.reason
	newRec.DurationMs = time.Since(start).Milliseconds()

	_ = rp.Store.Add(ctx, newRec)
	return newRec, nil
}

// rebuildRequest reconstructs an http.Request from a stored Record.
// Target URL is opaque ("/path"); the caller writes it over a direct
// connection to the local service.
func rebuildRequest(r *Record) (*http.Request, error) {
	body := bytes.NewReader(r.ReqBody)
	req, err := http.NewRequest(r.Method, r.Path, body)
	if err != nil {
		return nil, err
	}
	req.Header = cloneHeader(r.ReqHeaders)
	// Strip hop-by-hop so the downstream server doesn't see stale framing
	// from the original capture.
	req.Header.Del("Content-Length")
	req.Header.Del("Transfer-Encoding")
	req.ContentLength = int64(len(r.ReqBody))
	req.Host = r.Host
	return req, nil
}

type cappedResult struct {
	captured []byte
	total    int64
	reason   BodyReason
}

func readCapped(r io.Reader, max int) (cappedResult, error) {
	cr := newCapReader(r, max)
	// Drain fully so the downstream server sees the whole exchange.
	_, err := io.Copy(io.Discard, cr)
	body, total, reason := cr.Snapshot()
	return cappedResult{captured: body, total: total, reason: reason}, err
}
