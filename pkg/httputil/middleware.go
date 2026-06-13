package httputil

import (
	"context"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

// requestID is a global counter for unique request IDs. Per-process,
// not per-server: when one client request crosses two of our servers
// (newtcon → newtrun → newtron), the IDs are distinct but each appears
// in exactly one server's logs, which is what an operator needs to
// correlate a single hop.
var requestID atomic.Uint64

// RequestID adds a monotonically-increasing request ID to the
// X-Request-ID response header. Use it as the outermost middleware
// so the header is set on every response, including those from
// later middleware that short-circuits with an error.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID.Add(1)
		w.Header().Set("X-Request-ID", strconv.FormatUint(id, 10))
		next.ServeHTTP(w, r)
	})
}

// Logger logs every request after it completes. Method, path, status,
// and elapsed time — the four facts an operator scanning logs cares
// about. Formatted to match the existing newtron-server / newtrun-
// server convention exactly (test scripts grep for this format).
func Logger(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		})
	}
}

// Timeout caps every request at d. Handlers should observe
// r.Context().Done() to surrender cleanly. Used by newtron-server,
// which has long device-facing operations that need a deadline.
// Streaming endpoints (SSE) compose poorly with this middleware;
// route them outside the Timeout wrapper.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Recovery recovers from panics in handlers and writes a 500 with the
// standard JSON envelope. The panic stack trace goes to logger; the
// client sees only "internal server error".
func Recovery(logger *log.Logger) func(http.Handler) http.Handler {
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

// statusWriter wraps http.ResponseWriter to capture the status code
// for the Logger middleware and to forward Flush() so SSE handlers can
// flush event lines through the decorator chain.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
