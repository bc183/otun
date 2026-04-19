package inspect

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS requests (
    id            TEXT PRIMARY KEY,
    received_at   INTEGER NOT NULL,
    subdomain     TEXT NOT NULL DEFAULT '',
    host          TEXT NOT NULL DEFAULT '',
    method        TEXT NOT NULL,
    path          TEXT NOT NULL,
    proto         TEXT NOT NULL DEFAULT '',
    remote_addr   TEXT NOT NULL DEFAULT '',
    req_headers   BLOB,
    req_body      BLOB,
    req_size      INTEGER NOT NULL DEFAULT 0,
    req_reason    TEXT NOT NULL DEFAULT '',
    status        INTEGER NOT NULL DEFAULT 0,
    resp_headers  BLOB,
    resp_body     BLOB,
    resp_size     INTEGER NOT NULL DEFAULT 0,
    resp_reason   TEXT NOT NULL DEFAULT '',
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    err           TEXT NOT NULL DEFAULT '',
    replay        INTEGER NOT NULL DEFAULT 0,
    of_id         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_requests_received_at ON requests(received_at DESC);
CREATE INDEX IF NOT EXISTS idx_requests_id_desc ON requests(id DESC);
`

// SQLiteStore persists records in a SQLite database on disk.
type SQLiteStore struct {
	db *sql.DB

	subsMu sync.Mutex
	subs   map[int]chan *Record
	nextID int

	closed  chan struct{}
	closeMu sync.Mutex
}

// NewSQLiteStore opens a SQLite database at path (":memory:" for tests) and
// ensures the schema. All records are persisted indefinitely; callers manage
// retention out-of-band (backup/rotate the file, or VACUUM).
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite + WAL still prefers single writer
	if _, err := db.ExecContext(context.Background(), sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &SQLiteStore{
		db:     db,
		subs:   make(map[int]chan *Record),
		closed: make(chan struct{}),
	}, nil
}

// Add inserts a record.
func (s *SQLiteStore) Add(ctx context.Context, r *Record) error {
	reqHdr, _ := json.Marshal(r.ReqHeaders)
	respHdr, _ := json.Marshal(r.RespHeaders)

	replay := 0
	if r.Replay {
		replay = 1
	}

	_, err := s.db.ExecContext(ctx, `
        INSERT INTO requests (
            id, received_at, subdomain, host, method, path, proto, remote_addr,
            req_headers, req_body, req_size, req_reason,
            status, resp_headers, resp_body, resp_size, resp_reason,
            duration_ms, err, replay, of_id
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ReceivedAt.UnixNano(), r.Subdomain, r.Host, r.Method, r.Path, r.Proto, r.RemoteAddr,
		reqHdr, r.ReqBody, r.ReqSize, string(r.ReqReason),
		r.Status, respHdr, r.RespBody, r.RespSize, string(r.RespReason),
		r.DurationMs, r.Err, replay, r.OfID,
	)
	if err != nil {
		return fmt.Errorf("insert: %w", err)
	}

	s.broadcast(r)
	return nil
}

// List returns records newest-first.
func (s *SQLiteStore) List(ctx context.Context, f Filter) ([]*Record, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	var (
		where []string
		args  []any
	)
	if f.Cursor != "" {
		where = append(where, "id < ?")
		args = append(args, f.Cursor)
	}
	if f.Method != "" {
		where = append(where, "UPPER(method) = UPPER(?)")
		args = append(args, f.Method)
	}
	if f.StatusClass != 0 {
		where = append(where, "status >= ? AND status < ?")
		args = append(args, f.StatusClass*100, (f.StatusClass+1)*100)
	}
	if f.PathSub != "" {
		where = append(where, "path LIKE ?")
		args = append(args, "%"+f.PathSub+"%")
	}

	q := "SELECT " + selectCols + " FROM requests"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns a single record by ID.
func (s *SQLiteStore) Get(ctx context.Context, id string) (*Record, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+selectCols+" FROM requests WHERE id = ?", id)
	return scanRecord(row)
}

// Subscribe behaves like MemoryStore.Subscribe (in-process broadcast only).
func (s *SQLiteStore) Subscribe() (<-chan *Record, func()) {
	ch := make(chan *Record, 16)
	s.subsMu.Lock()
	id := s.nextID
	s.nextID++
	s.subs[id] = ch
	s.subsMu.Unlock()

	unsub := func() {
		s.subsMu.Lock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
		s.subsMu.Unlock()
	}
	return ch, unsub
}

func (s *SQLiteStore) broadcast(r *Record) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- r:
		default:
		}
	}
}

// Close shuts the store down.
func (s *SQLiteStore) Close() error {
	s.closeMu.Lock()
	select {
	case <-s.closed:
		s.closeMu.Unlock()
		return nil
	default:
		close(s.closed)
	}
	s.closeMu.Unlock()

	s.subsMu.Lock()
	for id, ch := range s.subs {
		delete(s.subs, id)
		close(ch)
	}
	s.subsMu.Unlock()

	return s.db.Close()
}

const selectCols = `id, received_at, subdomain, host, method, path, proto, remote_addr,
    req_headers, req_body, req_size, req_reason,
    status, resp_headers, resp_body, resp_size, resp_reason,
    duration_ms, err, replay, of_id`

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(sc scanner) (*Record, error) {
	var (
		r               Record
		receivedAtNs    int64
		reqHdr, respHdr []byte
		reqReason       string
		respReason      string
		replay          int
	)
	err := sc.Scan(
		&r.ID, &receivedAtNs, &r.Subdomain, &r.Host, &r.Method, &r.Path, &r.Proto, &r.RemoteAddr,
		&reqHdr, &r.ReqBody, &r.ReqSize, &reqReason,
		&r.Status, &respHdr, &r.RespBody, &r.RespSize, &respReason,
		&r.DurationMs, &r.Err, &replay, &r.OfID,
	)
	if err != nil {
		return nil, err
	}
	r.ReceivedAt = time.Unix(0, receivedAtNs)
	r.ReqReason = BodyReason(reqReason)
	r.RespReason = BodyReason(respReason)
	r.Replay = replay != 0
	if len(reqHdr) > 0 {
		_ = json.Unmarshal(reqHdr, &r.ReqHeaders)
	}
	if len(respHdr) > 0 {
		_ = json.Unmarshal(respHdr, &r.RespHeaders)
	}
	if r.ReqHeaders == nil {
		r.ReqHeaders = http.Header{}
	}
	if r.RespHeaders == nil {
		r.RespHeaders = http.Header{}
	}
	return &r, nil
}
