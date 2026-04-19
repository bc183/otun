// Package webui serves the client-side inspection UI (htmx + Alpine) over
// HTTP. Assets are embedded at build time; the server is expected to bind
// to a loopback address.
package webui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bc183/otun/internal/inspect"
	"github.com/charmbracelet/log"
)

//go:embed assets/*
var assets embed.FS

// Config configures the UI server.
type Config struct {
	Addr      string            // listen address, e.g. 127.0.0.1:4040
	Store     inspect.Store     // backing store
	Replayer  *inspect.Replayer // optional — if nil, replay is disabled
	TunnelURL func() string     // live lookup of the public tunnel URL
}

// Server is the webui HTTP server.
type Server struct {
	cfg    Config
	tmpl   *template.Template
	static http.Handler
	http   *http.Server
}

// New constructs a Server ready to be started with Run.
func New(cfg Config) (*Server, error) {
	tmpl, err := template.New("otun").Funcs(templateFuncs()).ParseFS(assets,
		"assets/list.html",
		"assets/detail.html",
	)
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:    cfg,
		tmpl:   tmpl,
		static: http.FileServer(http.FS(sub)),
	}
	return s, nil
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.static))
	mux.HandleFunc("GET /api/requests", s.listRequests)
	mux.HandleFunc("GET /api/requests/{id}", s.getRequest)
	mux.HandleFunc("POST /api/requests/{id}/replay", s.replayRequest)
	mux.HandleFunc("GET /api/stream", s.stream)

	s.http = &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- s.http.ListenAndServe() }()

	log.Info("inspect UI ready", "addr", "http://"+s.cfg.Addr)

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutCtx)
		return ctx.Err()
	case err := <-errc:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	html, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tunnelURL := ""
	if s.cfg.TunnelURL != nil {
		tunnelURL = s.cfg.TunnelURL()
	}
	// Inject the tunnel URL as a data attribute without re-templating.
	body := bytes.Replace(html, []byte("<body "), []byte(`<body data-tunnel-url="`+template.HTMLEscapeString(tunnelURL)+`" `), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) {
	f := inspect.Filter{
		Method:  r.URL.Query().Get("method"),
		PathSub: r.URL.Query().Get("q"),
		Cursor:  r.URL.Query().Get("cursor"),
		Limit:   100,
	}
	if sc := r.URL.Query().Get("status"); sc != "" {
		if n, err := strconv.Atoi(sc); err == nil {
			f.StatusClass = n
		}
	}

	recs, err := s.cfg.Store.List(r.Context(), f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, map[string]any{"records": recs})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "list.html", map[string]any{"Records": recs}); err != nil {
		log.Debug("list template error", "error", err)
	}
}

func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := s.cfg.Store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, rec)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "detail.html", map[string]any{"Record": rec}); err != nil {
		log.Debug("detail template error", "error", err)
	}
}

func (s *Server) replayRequest(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Replayer == nil {
		http.Error(w, "replay disabled", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	rec, err := s.cfg.Replayer.Replay(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, rec)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "detail.html", map[string]any{"Record": rec})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.cfg.Store.Subscribe()
	defer unsub()

	// Immediate heartbeat so clients know the stream is live.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case rec, ok := <-ch:
			if !ok {
				return
			}
			buf, _ := json.Marshal(rec)
			fmt.Fprintf(w, "event: request\ndata: %s\n\n", buf)
			flusher.Flush()
		}
	}
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// templateFuncs are the funcs available to our HTML templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"div": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"timefmt": func(t time.Time) string {
			return t.Local().Format("15:04:05")
		},
		"isJSON": func(h http.Header) bool {
			return strings.Contains(h.Get("Content-Type"), "application/json")
		},
		"prettyBody": func(h http.Header, body []byte) string {
			if len(body) == 0 {
				return ""
			}
			ct := h.Get("Content-Type")
			if strings.Contains(ct, "application/json") {
				var buf bytes.Buffer
				if err := indentJSON(&buf, body); err == nil {
					return buf.String()
				}
			}
			// Printable? Show as text. Otherwise hex preview.
			if isProbablyText(body) {
				return string(body)
			}
			return hexPreview(body, 1024)
		},
	}
}

func indentJSON(w io.Writer, body []byte) error {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func isProbablyText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	n := len(b)
	if n > 512 {
		n = 512
	}
	for i := 0; i < n; i++ {
		c := b[i]
		if c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func hexPreview(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%16 == 0 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "%02x ", c)
	}
	sb.WriteString(fmt.Sprintf("\n(showing %d bytes)", len(b)))
	return sb.String()
}
