// handler_audit.go — HTTP handlers for the audit-log inspector
// surface (issue #196 / auth-design.md L1+L6).
//
// Three endpoints, all read-only, all gated by PermAuditRead under
// the engage-when-configured pattern (CheckAuditReadGate; see
// authorization_ops.go):
//
//   GET /newtron/v1/networks/{netID}/audit/events?...        — paged, filtered
//   GET /newtron/v1/networks/{netID}/audit/events/{eventID}  — single event
//   GET /newtron/v1/networks/{netID}/audit/integrity         — hash-chain status
//
// Per-network scoping: the events and detail endpoints filter by the
// path's {netID} (Event.Network), so a caller authorized to read one
// network's audit sees only that network's events — the read scope
// matches the per-network authorization gate. The integrity endpoint
// still walks the whole (pre-partition) chain; per-network chains
// arrive when storage is partitioned per network.
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

// handleAuditEvent serves
// GET /newtron/v1/networks/{netID}/audit/events/{eventID} — the per-event
// detail view. Unlike the paged list (handleAuditEvents), it returns the full
// event including the redacted request body, so newtcon can show "what this
// operation submitted and changed" on a single clicked row. The change-set is
// already on the list event; the body is the field only this endpoint serves.
//
// Gating mirrors handleAuditEvents exactly — engage-when-configured
// PermAuditRead, Field stamp "audit_events" — so a where:{field:"audit_events"}
// clause scopes the list and the detail together. A missing eventID 404s via
// FindAuditEvent's NotFoundError.
func (s *Server) handleAuditEvent(w http.ResponseWriter, r *http.Request) {
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
	event, err := newtron.FindAuditEvent(s.auditLogPath, r.PathValue("eventID"))
	if err != nil {
		writeError(w, err)
		return
	}
	// Scope by network: an event whose Network differs from this path's
	// {netID} belongs to another network. Return the same NotFoundError
	// FindAuditEvent uses for a missing id — no existence leak across the
	// per-network read boundary. (Events with an empty Network — e.g. a
	// pre-scope global-log entry — belong to no network and are not served
	// through any network's endpoint.)
	if event.Network != r.PathValue("netID") {
		writeError(w, &newtron.NotFoundError{Resource: "audit event", Name: r.PathValue("eventID")})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, event)
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
		// Network is forced from the request path, never a client query
		// param — the per-network read boundary must not be widenable by a
		// caller supplying (or omitting) a network in the query string.
		Network:   r.PathValue("netID"),
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

	// Result order — newest-first by default; "asc" opts into chronological.
	switch v := q.Get("order"); v {
	case "", "asc", "desc":
		f.Order = v
	default:
		return f, &auditFilterParseError{Field: "order", Reason: "expected asc or desc"}
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
