package api

import (
	"context"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

// requestID is a global counter for unique request IDs.
var requestID atomic.Uint64

// contextKey is a private type for request-scoped values to avoid collisions
// with keys defined in other packages.
type contextKey string

const reqIDKey contextKey = "request_id"

// withRequestID adds a request ID to the context and response header. Matches
// the newtron-server convention so log lines correlate across services when
// newtrun-server is invoked by newtcon-server or by the CLI.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID.Add(1)
		ctx := context.WithValue(r.Context(), reqIDKey, id)
		w.Header().Set("X-Request-ID", strconv.FormatUint(id, 10))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withLogger logs each request after it completes.
func withLogger(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		})
	}
}

// withRecovery recovers from panics in handlers and returns a 500.
func withRecovery(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Printf("PANIC: %v\n%s", rec, debug.Stack())
					http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code for
// logging. The SSE handler bypasses this (it streams directly).
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer's Flush method when available, so
// SSE handlers can flush event lines through the wrapper. Without this, the
// withLogger / withRecovery decorators would mask the http.Flusher capability
// that the SSE handler requires.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
