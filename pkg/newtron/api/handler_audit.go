// handler_audit.go — HTTP handlers for the audit-log inspector
// surface (issue #196 / auth-design.md L1+L6).
//
// Two endpoints, both read-only, both gated by PermAuditRead under
// the engage-when-configured pattern (CheckAuditReadGate; see
// authorization_ops.go):
//
//   GET /newtron/v1/networks/{netID}/audit/events?...   — paged, filtered
//   GET /newtron/v1/networks/{netID}/audit/integrity    — hash-chain status
//
// The audit log path is operator-configured at startup
// (cmd/newt-server --audit-log <path>) and reaches this handler
// via api.Config.AuditLogPath → Server.auditLogPath. Empty path
// returns 404 — there is no audit log to inspect, so the endpoint
// has nothing to report.
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
)

// handleAuditEvents serves
// GET /newtron/v1/networks/{netID}/audit/events with query-string
// filtering and paging. Filter dimensions map 1:1 to
// newtron.AuditFilter / audit.Filter. The response is an
// AuditEventPage carrying the events for this page plus a total
// count for paging and an optional next_offset.
//
// Gating: PermAuditRead engage-when-configured — no audit.read
// entry in the grant table means the endpoint is ungated (preserves
// the existing CLI-equivalent reachability). The handler stamps
// Context.Field with "audit_events" so a where:{field:"audit_events"}
// clause scopes to this endpoint vs. the integrity-status one.
func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	if s.auditLogPath == "" {
		writeError(w, &newtron.NotFoundError{
			Resource: "audit log",
			Name:     "(unconfigured)",
		})
		return
	}
	authCtx := auth.NewContext().WithField("audit_events")
	if err := ne.net.CheckAuditReadGate(r.Context(), authCtx); err != nil {
		writeError(w, err)
		return
	}
	filter, err := parseAuditFilter(r)
	if err != nil {
		writeError(w, &newtron.ValidationError{Message: err.Error()})
		return
	}
	page, err := newtron.QueryAuditEvents(s.auditLogPath, filter)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, page)
}

// handleAuditIntegrity serves
// GET /newtron/v1/networks/{netID}/audit/integrity. Walks the hash
// chain end to end and returns AuditIntegrityResult. Pure read.
// Cheap on typical log sizes.
//
// Gating: same engage-when-configured pattern as handleAuditEvents.
// Field stamp is "audit_integrity" so a where:{field:"audit_integrity"}
// clause scopes to this endpoint.
func (s *Server) handleAuditIntegrity(w http.ResponseWriter, r *http.Request) {
	ne := s.requireNetwork(w, r)
	if ne == nil {
		return
	}
	if s.auditLogPath == "" {
		writeError(w, &newtron.NotFoundError{
			Resource: "audit log",
			Name:     "(unconfigured)",
		})
		return
	}
	authCtx := auth.NewContext().WithField("audit_integrity")
	if err := ne.net.CheckAuditReadGate(r.Context(), authCtx); err != nil {
		writeError(w, err)
		return
	}
	result, err := newtron.VerifyAuditIntegrity(s.auditLogPath)
	if err != nil {
		writeError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

// parseAuditFilter reads the query string into a newtron.AuditFilter.
// Every dimension is optional; missing fields are zero values which
// the underlying audit.Filter treats as "no constraint." Parse
// errors on type-coerced fields (Limit, Offset, time bounds,
// success/failure) surface as 400 validation errors so the client
// sees an actionable message rather than silently-applied defaults.
//
// Supported query params (all optional):
//
//	device, user, operation, service, interface — equality
//	resource                                     — equality (matches Filter.Resource if present)
//	since, until                                 — RFC3339 timestamps
//	success=true|false                           — drives SuccessOnly / FailureOnly
//	limit (default 100, max 1000)
//	offset (default 0)
func parseAuditFilter(r *http.Request) (newtron.AuditFilter, error) {
	q := r.URL.Query()
	f := newtron.AuditFilter{
		Device:    q.Get("device"),
		User:      q.Get("user"),
		Operation: q.Get("operation"),
		Service:   q.Get("service"),
		Interface: q.Get("interface"),
	}

	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, &auditFilterParseError{Field: "since", Reason: "expected RFC3339 timestamp"}
		}
		f.StartTime = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, &auditFilterParseError{Field: "until", Reason: "expected RFC3339 timestamp"}
		}
		f.EndTime = t
	}
	if v := q.Get("success"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, &auditFilterParseError{Field: "success", Reason: "expected true or false"}
		}
		if b {
			f.SuccessOnly = true
		} else {
			f.FailureOnly = true
		}
	}

	const defaultLimit = 100
	const maxLimit = 1000
	f.Limit = defaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return f, &auditFilterParseError{Field: "limit", Reason: "expected non-negative integer"}
		}
		if n > maxLimit {
			n = maxLimit
		}
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return f, &auditFilterParseError{Field: "offset", Reason: "expected non-negative integer"}
		}
		f.Offset = n
	}
	return f, nil
}

// auditFilterParseError carries an actionable message identifying
// which query parameter failed to parse. Implements the public
// error interface so writeError can map it to 400.
type auditFilterParseError struct {
	Field  string
	Reason string
}

func (e *auditFilterParseError) Error() string {
	return "audit filter: " + e.Field + ": " + e.Reason
}
