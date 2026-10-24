package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed web
var web embed.FS

// tokenHeader carries the token from the page to every API request.
const tokenHeader = "X-Kubeyard-Token"

// tokenPlaceholder is replaced with the token when the page is served.
const tokenPlaceholder = "__KUBEYARD_TOKEN__"

// contentSecurityPolicy lets the page load only its own scripts and styles.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'"

// server serves the UI and its API on one loopback listener. The API changes
// clusters, so it answers only requests for the listener's own host names that
// carry the token. A page from another origin cannot read the token (the
// same-origin policy), and a request with the token header needs a preflight
// that is never answered. The host check also stops DNS rebinding.
type server struct {
	token string
	hosts map[string]bool
	// writeMu serializes cluster writes, so the guard checked before a write
	// cannot race another write.
	writeMu sync.Mutex
}

// newServer creates a server for a listener at addr with a fresh token.
func newServer(addr net.Addr) (*server, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil, err
	}
	return &server{
		token: hex.EncodeToString(raw),
		hosts: map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true},
	}, nil
}

// handler returns the routes of the UI and the API.
func (s *server) handler() http.Handler {
	assets, err := fs.Sub(web, "web/static")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /api/contexts", s.contexts)
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("POST /api/flux", s.installFlux)
	mux.HandleFunc("POST /api/catalog-source", s.addSource)
	mux.HandleFunc("POST /api/addons/{name}/install", s.installAddon)
	mux.HandleFunc("POST /api/addons/{name}/update", s.updateAddon)
	mux.HandleFunc("POST /api/addons/{name}/uninstall", s.uninstallAddon)
	return s.guard(mux)
}

// guard refuses requests for other host names, and API requests without the
// token. Every response gets the headers that keep the page locked down.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			writeError(w, http.StatusForbidden, errors.New("host not allowed"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.validToken(r) {
			writeError(w, http.StatusUnauthorized, errors.New("missing or wrong token"))
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *server) validToken(r *http.Request) bool {
	got := []byte(r.Header.Get(tokenHeader))
	return subtle.ConstantTimeCompare(got, []byte(s.token)) == 1
}

// index serves the page with the token in its meta tag.
func (s *server) index(w http.ResponseWriter, r *http.Request) {
	page, err := web.ReadFile("web/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	page = bytes.Replace(page, []byte(tokenPlaceholder), []byte(s.token), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

// writeJSON writes v as the JSON body of a response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes err as the "error" field of a JSON response.
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// writeRequest is the body of a cluster write. Fields holds the install form:
// a boolean for a boolean parameter, text for every other one.
type writeRequest struct {
	Context string         `json:"context"`
	Fields  map[string]any `json:"fields"`
}

// decodeWrite reads a writeRequest. It writes the error response itself and
// reports false when the request is not usable.
func decodeWrite(w http.ResponseWriter, r *http.Request) (writeRequest, bool) {
	var req writeRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("请求格式错误：%w", err))
		return req, false
	}
	if req.Context == "" {
		writeError(w, http.StatusBadRequest, errors.New("缺少 context"))
		return req, false
	}
	return req, true
}

// write runs fn against the context in the background of the request. The
// response carries done on success, and label with the error on failure.
func (s *server) write(w http.ResponseWriter, contextName, label, done string, timeout time.Duration, fn action) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := runAction(contextName, timeout, fn); err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf("%s失败：%w", label, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": done})
}
