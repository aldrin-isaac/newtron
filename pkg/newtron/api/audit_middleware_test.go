package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
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
	// No caller resolved → recorded as an explicit anonymous (permissive-mode)
	// request, not the synthetic zero value.
	if evt.VerificationSource != audit.VerificationAnonymous {
		t.Errorf("VerificationSource = %q, want %q (no identity = anonymous)",
			evt.VerificationSource, audit.VerificationAnonymous)
	}
	if evt.User != "" {
		t.Errorf("User = %q, want empty for an anonymous request", evt.User)
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

// TestAuditMiddleware_CapturesBodyAndChanges covers the audit-content gap:
// the emitted event must carry (a) the request body the caller submitted,
// (b) the change-set the operation returned — and the handler must still see
// the full, untruncated body despite the middleware reading it first.
func TestAuditMiddleware_CapturesBodyAndChanges(t *testing.T) {
	cap := withCaptureLogger(t)

	var handlerSawBody string
	handler := auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		handlerSawBody = string(b)
		// Respond with a faithful device-write envelope so the middleware
		// can extract changes the same way it does in production.
		httputil.WriteJSON(w, http.StatusOK, newtron.WriteResult{
			ChangeCount: 1,
			Applied:     true,
			Changes: []sonic.ConfigChange{
				{
					Table:  "VLAN",
					Key:    "Vlan100",
					Type:   sonic.ChangeTypeModify,
					Fields: map[string]string{"description": "new"},
					From:   map[string]string{"description": "old"},
				},
			},
		})
	}))

	reqJSON := `{"vlan_id":100,"ssh_pass":"hunter2"}`
	req := httptest.NewRequest(http.MethodPost,
		"/newtron/v1/networks/default/nodes/switch1/create-vlan",
		strings.NewReader(reqJSON))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// The handler must have received the complete body (middleware tee must
	// not consume it).
	if handlerSawBody != reqJSON {
		t.Errorf("handler saw body %q; want full body %q", handlerSawBody, reqJSON)
	}

	if len(cap.events) != 1 {
		t.Fatalf("got %d events; want 1", len(cap.events))
	}
	evt := cap.events[0]
	if len(evt.Changes) != 1 || evt.Changes[0].Key != "Vlan100" {
		t.Errorf("evt.Changes = %+v; want one VLAN/Vlan100 change", evt.Changes)
	}
	// from/to (#236) survive the response→audit capture path.
	if evt.Changes[0].From["description"] != "old" {
		t.Errorf("evt.Changes[0].From = %v; want the prior {description:old}", evt.Changes[0].From)
	}
	if evt.Changes[0].Fields["description"] != "new" {
		t.Errorf("evt.Changes[0].Fields = %v; want the new {description:new}", evt.Changes[0].Fields)
	}
	// Request body recorded and the secret redacted.
	var recorded map[string]any
	if err := json.Unmarshal(evt.RequestBody, &recorded); err != nil {
		t.Fatalf("RequestBody not valid JSON: %v (%s)", err, evt.RequestBody)
	}
	if recorded["vlan_id"] != float64(100) {
		t.Errorf("recorded vlan_id = %v; want 100", recorded["vlan_id"])
	}
	if recorded["ssh_pass"] != redactedPlaceholder {
		t.Errorf("recorded ssh_pass = %v; want redacted", recorded["ssh_pass"])
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
