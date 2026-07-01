package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// auditPathValues extracts the {netID}, {node}, and {interface} segments
// from a newtron API request path
// (/newtron/v1/networks/<net>[/nodes/<node>[/interfaces/<iface>]]/...). The
// audit middleware uses this rather than r.PathValue because it runs outside a
// request-re-wrapping middleware (see emitMutationEvent) where PathValue is
// unavailable.
//
// netID is taken from its FIXED position (the segment after the
// `/newtron/v1/networks/` prefix), not by scanning for the literal
// "networks" — a network is validly named "networks" or "nodes"
// (idPattern), and a keyword scan would misroute its audit to another
// network's logger. node/interface follow their keyword positionally after
// netID; an absent dimension is "".
func auditPathValues(path string) (netID, node, iface string) {
	seg := strings.Split(strings.Trim(path, "/"), "/")
	// Anchor on the fixed prefix: newtron / v1 / networks / {netID}.
	if len(seg) < 4 || seg[0] != "newtron" || seg[2] != "networks" {
		return "", "", ""
	}
	netID = seg[3]
	// After {netID}: optional nodes/{node}[/interfaces/{iface}]. Scan the
	// remainder by keyword — node/interface names colliding with these
	// literals is astronomically less consequential than a netID collision
	// (they are informational fields, not the logger key).
	for i := 4; i+1 < len(seg); i++ {
		switch seg[i] {
		case "nodes":
			node = seg[i+1]
		case "interfaces":
			iface = seg[i+1]
		}
	}
	return netID, node, iface
}

// maxAuditCapture bounds how many bytes of the request and response bodies the
// middleware buffers for the audit record. Mutation payloads and their
// change-set responses are small relative to this; the cap exists so a
// pathological request can't balloon server memory. The handler always
// receives the full, untruncated body — only the stored audit copy is bounded.
const maxAuditCapture = 1 << 20 // 1 MiB

// auditLoggerResolver returns the audit logger for a request's network,
// keyed by the {netID} path value. Injected into auditMiddleware so the
// middleware is decoupled from the Server (and unit-testable in isolation);
// production passes Server.auditLoggerFor. A nil return — audit off, the
// network unregistered, or no {netID} (e.g. POST /networks) — makes emission
// a silent no-op.
type auditLoggerResolver func(netID string) audit.Logger

// auditMiddleware emits an audit.Event for every mutation request to the
// event's network audit log (auth-design.md L1). It resolves the network's
// logger via resolve(r.PathValue("netID")); a nil logger — the L1 disabled
// state or a network with no logger — makes emission a silent no-op. When a
// logger is present, every POST/PUT/DELETE request produces one Event after
// the handler returns, carrying the caller from audit.CallerFromContext, the
// HTTP method + URL as the operation, the URL path values (network, device,
// interface, etc.) as targets, and success/error derived from the response
// status.
//
// Read requests (GET, HEAD) and Server-Sent Events streams pass
// through unmodified — L1 covers mutation forensics, not query
// telemetry.
func auditMiddleware(resolve auditLoggerResolver, next http.Handler) http.Handler {
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
		emitMutationEvent(resolve, r, rw.status, start, reqBody, rw.body.Bytes())
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
func emitMutationEvent(resolve auditLoggerResolver, r *http.Request, status int, start time.Time, reqBody, respBody []byte) {
	// Extract path segments from r.URL.Path, NOT r.PathValue: this
	// middleware runs OUTSIDE middlewares (httputil.Timeout) that re-wrap
	// the request with a new context via r.WithContext, so the ServeMux
	// sets PathValue on a different *http.Request than the one reaching
	// here. r.URL survives the re-wrap. (Handlers inside the mux still use
	// r.PathValue correctly — only this outer middleware is affected.)
	netID, node, iface := auditPathValues(r.URL.Path)
	logger := resolve(netID)
	if logger == nil {
		// Audit off, network unregistered, or no {netID} (e.g. network
		// creation — a server-registry lifecycle act, logged operationally
		// by handleCreateNetwork, not in the per-network hashed chain).
		return
	}
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
		Network:            netID,
		Device:             node,
		Operation:          r.Method + " " + r.URL.Path,
		Interface:          iface,
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

	// Log to the resolved per-network logger. Errors are swallowed —
	// failing to emit an audit record must not fail the user's request.
	// Logger backends handle their own internal error recovery.
	_ = logger.Log(evt)
}
