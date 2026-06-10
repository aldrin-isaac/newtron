package audit

import "context"

// Caller is the request-scoped identity attached to the request
// context by the identity-extraction middleware. The audit middleware
// reads it when emitting an Event; downstream handlers may read it
// too once L3 wires authorization.
//
// Both fields are non-empty when an identity was attached. A nil
// *Caller (or a Caller with empty Username) means no identity was
// available — the listener accepted the request without an identity
// source matching its configured surfaces (Unix peer creds, TCP
// header, mTLS, PAM). Audit Events emitted for such requests carry
// an empty User and VerificationUnknown — a reviewer reading those
// can tell the difference between "no caller attached" (User empty,
// Source VerificationUnknown) and "caller present" (both set).
type Caller struct {
	Username string
	Source   VerificationSource
}

// callerCtxKey is the context key used by WithCaller / CallerFromContext.
// Unexported to prevent collisions with other packages' context keys.
type callerCtxKey struct{}

// WithCaller returns ctx with c attached. Used by the identity-
// extraction middleware once per request. A nil c is allowed — it
// represents "no identity attached at this listener for this request"
// and CallerFromContext will return nil for the resulting context.
func WithCaller(ctx context.Context, c *Caller) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, c)
}

// CallerFromContext returns the Caller attached by WithCaller, or
// nil if none was attached. Callers should handle nil — many test
// contexts and internal call paths won't have a Caller, and L1
// allows the no-identity case (the Event records User=""/
// VerificationUnknown so the gap is visible to a reviewer).
func CallerFromContext(ctx context.Context) *Caller {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(callerCtxKey{}).(*Caller)
	return c
}
