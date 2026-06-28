package api

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// maxAuditCapture bounds how many bytes of the request and response bodies the
// middleware buffers for the audit record. Mutation payloads and their
// change-set responses are small relative to this; the cap exists so a
// pathological request can't balloon server memory. The handler always
// receives the full, untruncated body — only the stored audit copy is bounded.
const maxAuditCapture = 1 << 20 // 1 MiB

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
		// Capture the request body before the handler consumes it, then hand
		// the handler an identical reader so it sees the full payload. The
		// captured copy (redacted) becomes the audit event's RequestBody.
		reqBody := captureRequestBody(r)
		rw := &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		emitMutationEvent(r, rw.status, start, reqBody, rw.body.Bytes())
	})
}

// captureRequestBody reads r.Body fully, replaces it with an equivalent reader
// so the handler still sees the complete payload, and returns the bytes for the
// audit record (capped at maxAuditCapture). Returns nil when there is no body
// or it can't be read — the request still proceeds; only the audit copy is
// affected. The handler always gets the untruncated body.
func captureRequestBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > maxAuditCapture {
		return body[:maxAuditCapture]
	}
	return body
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

// auditResponseWriter captures the response status code and body so the audit
// middleware can record success/failure and extract the change-set the handler
// returned. Wraps http.ResponseWriter; WriteHeader records the status and Write
// tees the bytes (capped at maxAuditCapture) while still forwarding them to the
// client unchanged.
type auditResponseWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *auditResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditResponseWriter) Write(b []byte) (int, error) {
	if remaining := maxAuditCapture - w.body.Len(); remaining > 0 {
		if len(b) <= remaining {
			w.body.Write(b)
		} else {
			w.body.Write(b[:remaining])
		}
	}
	return w.ResponseWriter.Write(b)
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
func emitMutationEvent(r *http.Request, status int, start time.Time, reqBody, respBody []byte) {
	caller := audit.CallerFromContext(r.Context())
	username := ""
	// No caller resolved means the request was processed anonymously: the
	// server required no identity for it (permissive mode). Record that
	// explicitly rather than leaving the zero value, which denotes a synthetic
	// event where the middleware never ran. Required-identity transports (mTLS
	// client CA, PAM) reject before this handler, so a real mutation reaching
	// here with no caller is anonymous-by-policy, not an anomaly.
	source := audit.VerificationAnonymous
	if caller != nil {
		username = caller.Username
		source = caller.Source
	}

	success := status >= 200 && status < 400
	evt := &audit.Event{
		Timestamp:          time.Now(),
		User:               username,
		VerificationSource: source,
		Device:             r.PathValue("node"),
		Operation:          r.Method + " " + r.URL.Path,
		Interface:          r.PathValue("interface"),
		Changes:            extractChanges(respBody),
		RequestBody:        redactRequestBody(reqBody),
		Success:            success,
		Duration:           time.Since(start),
		ClientIP:           r.RemoteAddr,
	}
	if !evt.Success {
		// Record the underlying failure reason (the envelope's `error`, the
		// same string the caller got live) so the audit trail is actionable;
		// fall back to the HTTP status text when the body carried no message.
		if msg := extractError(respBody); msg != "" {
			evt.Error = msg
		} else {
			evt.Error = http.StatusText(status)
		}
	}

	// Log via the default logger. When no logger is configured
	// (L1 disabled), audit.Log is a silent no-op. Logger errors
	// are swallowed — failing to emit an audit record must not
	// fail the user's request. Logger backends are responsible
	// for their own internal error handling (retries, fallbacks).
	_ = audit.Log(evt)
}
