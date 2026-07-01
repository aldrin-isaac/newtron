package audit

import "time"

// DecisionOperationPrefix is prepended to the permission name to
// form the Operation field on a decision event. Reviewers can filter
// the audit log by "authcheck:" to see only authorization decisions,
// distinguishing them from L1 request-level events whose Operation
// is the HTTP method + path.
const DecisionOperationPrefix = "authcheck:"

// Decision is the per-call input shape for LogDecision. It mirrors
// the auth.Context dimensions plus the L1-verified identity, all
// flattened to strings so the audit package doesn't import the auth
// package (which would create a cycle: auth → audit via the request
// context already, and audit → auth via this decision shape would
// close it).
//
// Permission is the action that was checked (e.g. "device.write").
// Caller and Source come from the verified identity (L1/L2).
// Device, Service, Interface, Resource, Field are the populated
// dimensions of auth.Context at decision time — Reviewers reconstruct
// the L5 where-clause evaluation from these. Error is the decision
// result: nil = allow, non-nil = deny (and the error's message is
// recorded on the event).
type Decision struct {
	Permission string
	Caller     string
	Source     VerificationSource
	// Network is the network the permission was evaluated against —
	// stamped onto the emitted event so the per-network audit read path
	// scopes authorization decisions to their network, matching
	// request-level events (Event.Network).
	Network    string
	Device     string
	Service    string
	Interface  string
	Resource   string
	Field      string
	Error      error
}

// LogDecision emits an audit Event for one Network.checkPermission
// call (auth-design.md L3+L5). One event per decision — allow and
// deny alike — so a reviewer reading the audit log can answer
// "did authorization happen, who got allowed, who got denied, and
// against which L5 context dimensions" without inferring from the
// request-level event alone.
//
// Like Log, LogDecision is a silent no-op when no default logger is
// configured (auth-design.md L1 disabled state preserved). A
// permission decision is still made; it just doesn't get recorded.
//
// The Event ID field stays empty, matching the L1 request-level
// events emitted by auditMiddleware — populating IDs is L1
// hardening, not L3 scope.
func LogDecision(d Decision) {
	if getDefaultLogger() == nil {
		return
	}
	event := &Event{
		Timestamp:          time.Now().UTC(),
		User:               d.Caller,
		VerificationSource: d.Source,
		Network:            d.Network,
		Device:             d.Device,
		Operation:          DecisionOperationPrefix + d.Permission,
		Service:            d.Service,
		Interface:          d.Interface,
		Resource:           d.Resource,
		Field:              d.Field,
		Success:            d.Error == nil,
	}
	if d.Error != nil {
		event.Error = d.Error.Error()
	}
	_ = Log(event)
}
