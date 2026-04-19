// Package inspect captures, stores, and replays HTTP requests that pass
// through the tunnel client.
package inspect

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"net/http"
	"time"
)

// BodyReason describes why a body was truncated.
type BodyReason string

const (
	BodyFull     BodyReason = ""
	BodyStreamed BodyReason = "streamed"
	BodyTooLarge BodyReason = "too_large"
	BodyUpgrade  BodyReason = "upgrade"
)

// Record is a single captured request/response pair.
type Record struct {
	ID         string      `json:"id"`
	ReceivedAt time.Time   `json:"received_at"`
	Subdomain  string      `json:"subdomain,omitempty"`
	Host       string      `json:"host"`
	Method     string      `json:"method"`
	Path       string      `json:"path"`
	Proto      string      `json:"proto,omitempty"`
	RemoteAddr string      `json:"remote_addr,omitempty"`
	ReqHeaders http.Header `json:"req_headers,omitempty"`
	ReqBody    []byte      `json:"req_body,omitempty"`
	ReqSize    int64       `json:"req_size"`
	ReqReason  BodyReason  `json:"req_reason,omitempty"`

	Status      int         `json:"status"`
	RespHeaders http.Header `json:"resp_headers,omitempty"`
	RespBody    []byte      `json:"resp_body,omitempty"`
	RespSize    int64       `json:"resp_size"`
	RespReason  BodyReason  `json:"resp_reason,omitempty"`

	DurationMs int64  `json:"duration_ms"`
	Err        string `json:"err,omitempty"`

	Replay bool   `json:"replay,omitempty"`
	OfID   string `json:"of_id,omitempty"`
}

// Filter narrows a List query.
type Filter struct {
	Method      string // exact, case-insensitive; "" = any
	StatusClass int    // 2|3|4|5 for 2xx etc; 0 = any
	PathSub     string // substring match on path
	Limit       int    // max rows; 0 = default (100)
	Cursor      string // record ID; returned page has IDs strictly older than this
}

// Store is the persistence interface for records.
// Implementations must be safe for concurrent use.
type Store interface {
	Add(ctx context.Context, r *Record) error
	List(ctx context.Context, f Filter) ([]*Record, error)
	Get(ctx context.Context, id string) (*Record, error)
	// Subscribe returns a channel that receives records as they're added and
	// an unsubscribe function. The channel buffer is small; slow consumers
	// may miss records.
	Subscribe() (<-chan *Record, func())
	Close() error
}

// NewID returns a time-sortable 16-character ID built from the current time
// plus randomness. Later IDs sort greater than earlier ones.
func NewID() string {
	// 6 bytes of ms-resolution time + 4 bytes of random = 10 bytes → 16 base32 chars
	now := time.Now().UnixMilli()
	var b [10]byte
	b[0] = byte(now >> 40)
	b[1] = byte(now >> 32)
	b[2] = byte(now >> 24)
	b[3] = byte(now >> 16)
	b[4] = byte(now >> 8)
	b[5] = byte(now)
	rand.Read(b[6:])
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
}

// matches reports whether the record passes the filter.
func (f Filter) matches(r *Record) bool {
	if f.Method != "" && !equalFold(f.Method, r.Method) {
		return false
	}
	if f.StatusClass != 0 {
		if r.Status/100 != f.StatusClass {
			return false
		}
	}
	if f.PathSub != "" && !contains(r.Path, f.PathSub) {
		return false
	}
	return true
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
