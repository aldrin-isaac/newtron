package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// DecisionOperationPrefix is prepended to the permission name to
// form the Operation field on a decision event. Reviewers can filter
// the audit log by "authcheck:" to see only authorization decisions,
// distinguishing them from L1 request-level events whose Operation
// is the handler name.
const DecisionOperationPrefix = "authcheck:"

// LogDecision emits an audit Event for one Network.checkPermission
// call (auth-design.md L3). One event per decision — allow and deny
// alike — so a reviewer reading the audit log can answer "did
// authorization happen, who got allowed, who got denied" without
// inferring from request-level success.
//
// The event's User and VerificationSource come from the verified
// identity attached to ctx by the HTTP boundary. resource is the
// scoping resource the permission is being checked against (service
// name, profile name, etc.) — surfaced so a reviewer doesn't have to
// cross-reference the corresponding L1 event to learn what was
// being acted on.
//
// Like Log, LogDecision is a silent no-op when no default logger is
// configured (audit-design.md L1 disabled state preserved). A
// permission decision is still made; it just doesn't get recorded.
func LogDecision(ctx context.Context, permission, caller string, source VerificationSource, resource string, decision error) {
	if getDefaultLogger() == nil {
		return
	}
	event := &Event{
		ID:                 newEventID(),
		Timestamp:          time.Now().UTC(),
		User:               caller,
		VerificationSource: source,
		Operation:          DecisionOperationPrefix + permission,
		Service:            resource,
		Success:            decision == nil,
	}
	if decision != nil {
		event.Error = decision.Error()
	}
	_ = Log(event)
	_ = ctx // ctx is reserved for future fields (request ID linkage, deadline)
}

// newEventID returns a hex-encoded random ID for an audit event.
// Decision events use the same shape as request-level events so
// downstream consumers can treat them uniformly.
func newEventID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
