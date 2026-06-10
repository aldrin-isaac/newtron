package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// captureLogger is an audit.Logger that records every Event written
// to it. Tests inspect Events to assert the audit middleware emitted
// what it should have.
type captureLogger struct {
	mu     sync.Mutex
	events []*audit.Event
}

func (c *captureLogger) Log(e *audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *captureLogger) Query(_ audit.Filter) ([]*audit.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*audit.Event, len(c.events))
	copy(out, c.events)
	return out, nil
}

func (c *captureLogger) Close() error { return nil }

// withCaptureLogger installs a fresh captureLogger as the default
// audit logger for the duration of the test, restoring whatever was
// there before on cleanup. Returns the capture so tests can inspect
// the recorded events.
func withCaptureLogger(t *testing.T) *captureLogger {
	t.Helper()
	cap := &captureLogger{}
	// Save the existing default logger (whatever it is — almost
	// certainly nil) and restore on cleanup so test order doesn't
	// affect other audit-dependent tests.
	prev := audit.Query
	_ = prev // not strictly needed; SetDefaultLogger is what we
	// must reset. There's no GetDefaultLogger — restore to nil,
	// which is the default test state.
	audit.SetDefaultLogger(cap)
	t.Cleanup(func() { audit.SetDefaultLogger(nil) })
	return cap
}

// TestAuditMiddleware_EmitsOnMutationRequests covers the L1 happy
// path: a POST handler returns 200; the audit middleware emits one
// Event with Method+URL as Operation and Success=true.
func TestAuditMiddleware_EmitsOnMutationRequests(t *testing.T) {
	cap := withCaptureLogger(t)

	handler := auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks/default/nodes/switch1/vlans", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if len(cap.events) != 1 {
		t.Fatalf("got %d events; want 1", len(cap.events))
	}
	evt := cap.events[0]
	if evt.Operation != "POST /newtron/v1/networks/default/nodes/switch1/vlans" {
		t.Errorf("Operation = %q, unexpected", evt.Operation)
	}
	if !evt.Success {
		t.Errorf("Success = false, want true")
	}
}

// TestAuditMiddleware_SkipsReads pins that GET requests do not
// produce audit events — L1 scope is mutation forensics, not
// query telemetry.
func TestAuditMiddleware_SkipsReads(t *testing.T) {
	cap := withCaptureLogger(t)

	handler := auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/x", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	if len(cap.events) != 0 {
		t.Errorf("got %d events for read methods; want 0", len(cap.events))
	}
}

// TestAuditMiddleware_FailureSetsSuccessFalse pins that a 5xx
// response is recorded with Success=false and an Error field
// populated, so a reviewer can find failed mutations.
func TestAuditMiddleware_FailureSetsSuccessFalse(t *testing.T) {
	cap := withCaptureLogger(t)

	handler := auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	req := httptest.NewRequest(http.MethodDelete, "/newtron/v1/networks/default/nodes/switch1/vlans/100", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if len(cap.events) != 1 {
		t.Fatalf("got %d events; want 1", len(cap.events))
	}
	evt := cap.events[0]
	if evt.Success {
		t.Errorf("Success = true on 500; want false")
	}
	if evt.Error == "" {
		t.Errorf("Error field empty on failure; want populated")
	}
}

// TestAuditMiddleware_NoLoggerIsNoOp pins the L1 disabled state:
// when no default audit logger is configured, the middleware is a
// silent passthrough — handlers run, no events are queued, no
// panics, no errors.
func TestAuditMiddleware_NoLoggerIsNoOp(t *testing.T) {
	// Explicitly clear the default logger for this test only.
	audit.SetDefaultLogger(nil)
	t.Cleanup(func() { audit.SetDefaultLogger(nil) })

	handlerCalled := false
	handler := auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !handlerCalled {
		t.Error("handler was not called when audit logger is disabled")
	}
}

// TestAuditMiddleware_PropagatesCallerFromContext pins that the
// audit event carries the caller from the request context (set by
// callerMiddleware). Together with the caller-middleware tests this
// asserts the full L1 identity pipeline.
func TestAuditMiddleware_PropagatesCallerFromContext(t *testing.T) {
	cap := withCaptureLogger(t)

	chain := callerMiddleware("X-Newtron-Caller")(auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("X-Newtron-Caller", "alice")
	chain.ServeHTTP(httptest.NewRecorder(), req)

	if len(cap.events) != 1 {
		t.Fatalf("got %d events; want 1", len(cap.events))
	}
	evt := cap.events[0]
	if evt.User != "alice" {
		t.Errorf("User = %q, want alice", evt.User)
	}
	if evt.VerificationSource != audit.VerificationSelfAttestedHeader {
		t.Errorf("VerificationSource = %q, want %q",
			evt.VerificationSource, audit.VerificationSelfAttestedHeader)
	}
}
