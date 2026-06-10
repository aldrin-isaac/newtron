package api

import (
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// auditMiddleware emits an audit.Event for every mutation request
// (auth-design.md L1). The behavior is governed by the default audit
// logger (audit.SetDefaultLogger): when no logger is configured —
// the L1 disabled state — emission is a silent no-op. When a logger
// is configured, every POST/PUT/DELETE request produces one Event
// after the handler returns, carrying the caller from
// audit.CallerFromContext, the HTTP method + URL as the operation,
// the URL path values (device, interface, etc.) as targets, and
// success/error derived from the response status.
//
// Read requests (GET, HEAD) and Server-Sent Events streams pass
// through unmodified — L1 covers mutation forensics, not query
// telemetry.
func auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutation(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rw := &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		emitMutationEvent(r, rw.status, start)
	})
}

// isMutation reports whether the HTTP method indicates a state-
// changing request that should be audited. PATCH is included even
// though the codebase doesn't currently use it — if a future handler
// adopts PATCH for partial updates, audit will pick it up
// automatically.
func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	}
	return false
}

// auditResponseWriter captures the response status code so the audit
// middleware can record success/failure after the handler returns.
// Wraps http.ResponseWriter; the WriteHeader override is the only
// behavior change.
type auditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// emitMutationEvent constructs and logs an audit.Event for the
// completed mutation request. Caller comes from the request context
// (populated by callerMiddleware); when nil, the Event records
// User="" and VerificationUnknown so a reviewer sees the
// no-identity case explicitly.
//
// Operation is the HTTP method + URL path. Device/Interface/Service
// dimensions are extracted from the URL path values when present —
// when the handler's route registration carries those names, they're
// available via r.PathValue without further parsing.
func emitMutationEvent(r *http.Request, status int, start time.Time) {
	caller := audit.CallerFromContext(r.Context())
	username := ""
	source := audit.VerificationUnknown
	if caller != nil {
		username = caller.Username
		source = caller.Source
	}

	evt := &audit.Event{
		Timestamp:          time.Now(),
		User:               username,
		VerificationSource: source,
		Device:             r.PathValue("device"),
		Operation:          r.Method + " " + r.URL.Path,
		Interface:          r.PathValue("interface"),
		Success:            status >= 200 && status < 400,
		Duration:           time.Since(start),
		ClientIP:           r.RemoteAddr,
	}
	if !evt.Success {
		evt.Error = http.StatusText(status)
	}

	// Log via the default logger. When no logger is configured
	// (L1 disabled), audit.Log is a silent no-op. Logger errors
	// are swallowed — failing to emit an audit record must not
	// fail the user's request. Logger backends are responsible
	// for their own internal error handling (retries, fallbacks).
	_ = audit.Log(evt)
}
