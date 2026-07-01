package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// auditServeGet constructs a Server with per-network audit enabled iff
// auditOn (audit storage is per-network now — the log lives in the
// network's own folder, audit.Path(specDir)), registers a network from
// specDir as "default", and serves the GET. Seed events with seedAuditLog
// before calling when auditOn. Used by all audit-endpoint tests so the
// wiring is consistent.
func auditServeGet(t *testing.T, specDir string, auditOn bool, path string) *httptest.ResponseRecorder {
	t.Helper()
	s := NewServer(Config{Audit: auditOn})
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

// scaffoldAuditNetwork builds an empty network dir suitable for
// audit-endpoint tests. The network needs no spec content for the
// endpoints to work — they only consult the audit log and the
// authorization table.
func scaffoldAuditNetwork(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := spec.CreateEmpty(dir, "audit-endpoint test fixture"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	return dir
}

// seedAuditLog writes integrity-chained events into specDir's audit log
// (audit.Path(specDir)) — the location the per-network read path reads.
// Returns that path for tests that manipulate the file directly (tamper).
//
// Events with no Network default to "default" — the network every
// audit-endpoint test here registers and serves. The middleware always
// stamps Network, so a seed reflects "these are default's events." A test
// probing cross-network scoping sets Network explicitly and is untouched.
func seedAuditLog(t *testing.T, specDir string, events []audit.Event) string {
	t.Helper()
	path := audit.Path(specDir)
	logger, err := audit.NewFileLoggerWithIntegrity(path, audit.RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLoggerWithIntegrity: %v", err)
	}
	for i := range events {
		if events[i].Network == "" {
			events[i].Network = "default"
		}
		if err := logger.Log(&events[i]); err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}
	_ = logger.Close()
	return path
}

// writeAuditLog scaffolds a "default" network dir, seeds its audit log
// (integrity-chained) with events, and returns the dir — the value tests
// pass to auditServeGet. Audit storage is per-network, so the seed and the
// served network are one location (audit.Path(dir)).
func writeAuditLog(t *testing.T, events []audit.Event) string {
	t.Helper()
	dir := scaffoldAuditNetwork(t)
	seedAuditLog(t, dir, events)
	return dir
}

// TestAuditEvents_NoAuditLogConfigured pins the "audit endpoint
// returns 404 when --audit was never set" contract. There is
// no log to inspect; the endpoint truthfully reports it.
func TestAuditEvents_NoAuditLogConfigured(t *testing.T) {
	dir := scaffoldAuditNetwork(t)
	w := auditServeGet(t, dir, false, "/newtron/v1/networks/default/audit/events")
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// TestAuditIntegrity_NoAuditLogConfigured: same shape on the
// integrity endpoint.
func TestAuditIntegrity_NoAuditLogConfigured(t *testing.T) {
	dir := scaffoldAuditNetwork(t)
	w := auditServeGet(t, dir, false, "/newtron/v1/networks/default/audit/integrity")
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// TestAuditEvents_ReturnsPage exercises the happy path: log file
// exists with N entries, GET returns the page + total + no
// next_offset (because limit defaulted to 100 and entry count < 100).
//
// §16: hand-verified events vs. response. The wire shape must
// carry exactly the fields the operator UI will render (timestamp,
// user, device, operation, success) — not a derived summary.
func TestAuditEvents_ReturnsPage(t *testing.T) {
	logPath := writeAuditLog(t, []audit.Event{
		{User: "alice", Device: "switch1", Operation: "POST /create-vlan", Success: true},
		{User: "bob", Device: "switch1", Operation: "POST /create-vrf", Success: true},
		{User: "mallory", Device: "switch1", Operation: "POST /delete-acl", Success: false, Error: "permission denied"},
	})
	w := auditServeGet(t, logPath, true, "/newtron/v1/networks/default/audit/events")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Events     []map[string]any `json:"events"`
			Total      int              `json:"total"`
			NextOffset *int             `json:"next_offset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	if envelope.Data.Total != 3 {
		t.Errorf("total: got %d, want 3", envelope.Data.Total)
	}
	if len(envelope.Data.Events) != 3 {
		t.Errorf("events len: got %d, want 3", len(envelope.Data.Events))
	}
	if envelope.Data.NextOffset != nil {
		t.Errorf("next_offset: got %v, want nil (limit=100 default > 3 entries)", *envelope.Data.NextOffset)
	}
	// Spot-check field presence on the first event.
	got := envelope.Data.Events[0]
	for _, key := range []string{"user", "device", "operation", "success", "timestamp"} {
		if _, ok := got[key]; !ok {
			t.Errorf("event missing key %q in response: %v", key, got)
		}
	}
}

// TestAuditEvents_PerNetworkScope pins that each network's endpoint returns
// ONLY its own events — the list AND the single-event-by-id detail. With
// per-network storage each network has its OWN audit file, so a caller
// authorized for net-a physically cannot reach net-b's log; the cross-network
// detail fetch 404s because net-b's event isn't in net-a's file. (The scope
// leak this guards against — a caller authorized for A reading B's events —
// was possible when one global file backed every network's gated endpoint.)
func TestAuditEvents_PerNetworkScope(t *testing.T) {
	dirA := scaffoldAuditNetwork(t)
	dirB := scaffoldAuditNetwork(t)
	seedAuditLog(t, dirA, []audit.Event{{Network: "net-a", User: "alice", Operation: "POST /op-a", Success: true}})
	seedAuditLog(t, dirB, []audit.Event{{Network: "net-b", User: "bob", Operation: "POST /op-b", Success: true}})

	s := NewServer(Config{Audit: true})
	if err := s.RegisterNetwork("net-a", dirA); err != nil {
		t.Fatalf("RegisterNetwork net-a: %v", err)
	}
	if err := s.RegisterNetwork("net-b", dirB); err != nil {
		t.Fatalf("RegisterNetwork net-b: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	serve := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		return w
	}
	decodeEvents := func(w *httptest.ResponseRecorder) []map[string]any {
		t.Helper()
		if w.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				Events []map[string]any `json:"events"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v; body: %s", err, w.Body.String())
		}
		return env.Data.Events
	}

	// net-a's endpoint returns only alice's event, never bob's.
	aEvents := decodeEvents(serve("/newtron/v1/networks/net-a/audit/events"))
	if len(aEvents) != 1 || aEvents[0]["user"] != "alice" {
		t.Fatalf("net-a events: got %v, want exactly [alice]", aEvents)
	}

	// net-b's endpoint returns only bob's event, and yields its id.
	bEvents := decodeEvents(serve("/newtron/v1/networks/net-b/audit/events"))
	if len(bEvents) != 1 || bEvents[0]["user"] != "bob" {
		t.Fatalf("net-b events: got %v, want exactly [bob]", bEvents)
	}
	bID, _ := bEvents[0]["id"].(string)
	if bID == "" {
		t.Fatal("net-b event has no id (integrity chain should populate it)")
	}

	// The single-event detail path is scoped too: net-b's event fetched
	// through net-a's endpoint is 404 (no existence leak), but net-b can
	// fetch its own.
	if w := serve("/newtron/v1/networks/net-a/audit/events/" + bID); w.Code != http.StatusNotFound {
		t.Errorf("cross-network detail fetch: got %d, want 404; body: %s", w.Code, w.Body.String())
	}
	if w := serve("/newtron/v1/networks/net-b/audit/events/" + bID); w.Code != http.StatusOK {
		t.Errorf("own-network detail fetch: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// TestAudit_FullCycle_MutationToReadEndpoint is the end-to-end guard: a real
// mutation through the FULL middleware chain (including httputil.Timeout, which
// re-wraps the request — the exact layering that hid the netID-from-PathValue
// bug) must land in the network's own FileLogger and read back through the
// per-network audit endpoint with network+device stamped. This is the test
// whose absence let that bug reach manual E2E instead of CI.
func TestAudit_FullCycle_MutationToReadEndpoint(t *testing.T) {
	specDir := copyTestSpecDir(t)
	s := NewServer(Config{Audit: true})
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(t.Context()) })

	// Real mutation (topology mode → in-memory, no device needed).
	w := httpPostJSON(t, s, "/newtron/v1/networks/default/nodes/switch1/create-vlan?mode=topology",
		map[string]any{"id": 777})
	if w.Code != http.StatusCreated {
		t.Fatalf("mutation: got %d, want 201; body: %s", w.Code, w.Body.String())
	}

	// Read it back through the per-network audit endpoint.
	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/audit/events", nil)
	rw := httptest.NewRecorder()
	s.Handler().ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("audit read: got %d, want 200; body: %s", rw.Code, rw.Body.String())
	}
	var env struct {
		Data struct {
			Events []map[string]any `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, rw.Body.String())
	}
	if len(env.Data.Events) != 1 {
		t.Fatalf("audit events: got %d, want 1 (the create-vlan mutation)", len(env.Data.Events))
	}
	e := env.Data.Events[0]
	if e["network"] != "default" {
		t.Errorf("event network = %v, want default (stamped from the URL, surviving the Timeout re-wrap)", e["network"])
	}
	if e["device"] != "switch1" {
		t.Errorf("event device = %v, want switch1", e["device"])
	}
	if op, _ := e["operation"].(string); !strings.Contains(op, "create-vlan") {
		t.Errorf("event operation = %v, want …/create-vlan", e["operation"])
	}
}

// closeCountingLogger is a captureLogger that counts Close calls, so a test
// can assert the audit logger's lifecycle (survives reload, closed on
// unregister).
type closeCountingLogger struct {
	captureLogger
	closes int
}

func (c *closeCountingLogger) Close() error { c.closes++; return nil }

// TestReloadNetwork_KeepsAuditLogger pins the reload lifecycle: a spec reload
// changes specs, not the audit ledger, so the network's audit logger is
// carried forward (same instance, NOT closed) — closing and reopening would
// race an in-flight mutation's already-fetched logger reference and lose the
// event. Unregister, by contrast, DOES close it (terminal transition).
func TestReloadNetwork_KeepsAuditLogger(t *testing.T) {
	dir := scaffoldAuditNetwork(t)
	s := NewServer(Config{}) // audit off → no real FileLogger opened; we inject one
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	cap := &closeCountingLogger{}
	s.networks["default"].auditLogger = cap
	s.networks["default"].net.SetAuditLogger(cap)

	if err := s.ReloadNetwork("default"); err != nil {
		t.Fatalf("ReloadNetwork: %v", err)
	}
	if cap.closes != 0 {
		t.Errorf("audit logger closed %d times on reload; want 0 (must survive)", cap.closes)
	}
	if s.networks["default"].auditLogger != cap {
		t.Error("reload replaced the audit logger; want the same instance carried forward")
	}

	if err := s.UnregisterNetwork("default"); err != nil {
		t.Fatalf("UnregisterNetwork: %v", err)
	}
	if cap.closes != 1 {
		t.Errorf("audit logger closed %d times after unregister; want exactly 1", cap.closes)
	}
}

// TestCreateNetwork_NoHashedAuditEntry pins that network creation — a
// server-registry lifecycle act, not a network-scoped mutation — leaves no
// entry in the per-network hashed chain. POST /networks carries no {netID},
// so the mutation middleware resolves no logger and emits nothing; the new
// network's audit endpoint therefore returns zero events. ("Who created it"
// is answerable via the operational log, not the chain.)
func TestCreateNetwork_NoHashedAuditEntry(t *testing.T) {
	base := t.TempDir()
	s := NewServer(Config{Audit: true, NetworksBase: base})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	create := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", strings.NewReader(`{"id":"foo"}`))
	cw := httptest.NewRecorder()
	s.Handler().ServeHTTP(cw, create)
	if cw.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body: %s", cw.Code, cw.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/foo/audit/events", nil)
	gw := httptest.NewRecorder()
	s.Handler().ServeHTTP(gw, get)
	if gw.Code != http.StatusOK {
		t.Fatalf("audit events: got %d, want 200; body: %s", gw.Code, gw.Body.String())
	}
	var env struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(gw.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body: %s", err, gw.Body.String())
	}
	if env.Data.Total != 0 {
		t.Errorf("create-network produced %d hashed audit event(s); want 0", env.Data.Total)
	}
}

// TestAuditEvents_ExposesVerificationSource pins that the public wire shape
// carries verification_source so a reviewer can tell a verified identity from a
// self-attested one — and, crucially, an anonymous (permissive-mode) request
// from a missing-data defect.
func TestAuditEvents_ExposesVerificationSource(t *testing.T) {
	logPath := writeAuditLog(t, []audit.Event{
		{User: "alice", Operation: "op1", Success: true, VerificationSource: audit.VerificationPAM},
		{User: "", Operation: "op2", Success: true, VerificationSource: audit.VerificationAnonymous},
	})
	w := auditServeGet(t, logPath, true, "/newtron/v1/networks/default/audit/events?order=asc")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Events []map[string]any `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Events) != 2 {
		t.Fatalf("events: got %d, want 2", len(env.Data.Events))
	}
	if got := env.Data.Events[0]["verification_source"]; got != "pam" {
		t.Errorf("event 0 verification_source = %v, want pam", got)
	}
	if got := env.Data.Events[1]["verification_source"]; got != "anonymous" {
		t.Errorf("event 1 verification_source = %v, want anonymous (permissive-mode record)", got)
	}
}

// TestAuditEvent_Detail covers the per-event detail endpoint
// (GET …/audit/events/{id}): the list omits the request body (lean), the
// detail endpoint serves it, and an unknown id is a 404.
func TestAuditEvent_Detail(t *testing.T) {
	logPath := writeAuditLog(t, []audit.Event{{
		User:        "alice",
		Device:      "switch1",
		Operation:   "POST /create-vlan",
		Success:     true,
		RequestBody: json.RawMessage(`{"vlan_id":100}`),
		Changes:     []node.Change{{Table: "VLAN", Key: "Vlan100", Type: sonic.ChangeTypeAdd}},
	}})
	base := "/newtron/v1/networks/default/audit/events"

	// List: must NOT carry the request body (lean), but should carry changes.
	listW := auditServeGet(t, logPath, true, base)
	var list struct {
		Data struct {
			Events []map[string]any `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Data.Events) != 1 {
		t.Fatalf("list events: got %d, want 1", len(list.Data.Events))
	}
	if _, present := list.Data.Events[0]["request_body"]; present {
		t.Errorf("list event carried request_body; want it omitted (lean list)")
	}
	id, _ := list.Data.Events[0]["id"].(string)
	if id == "" {
		t.Fatalf("list event has no id to fetch detail by")
	}

	// Detail: must carry the request body and changes.
	detailW := auditServeGet(t, logPath, true, base+"/"+id)
	if detailW.Code != http.StatusOK {
		t.Fatalf("detail status: got %d, want 200; body: %s", detailW.Code, detailW.Body.String())
	}
	var detail struct {
		Data struct {
			RequestBody json.RawMessage  `json:"request_body"`
			Changes     []map[string]any `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(detailW.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v; body: %s", err, detailW.Body.String())
	}
	if string(detail.Data.RequestBody) != `{"vlan_id":100}` {
		t.Errorf("detail request_body: got %s, want the recorded body", detail.Data.RequestBody)
	}
	if len(detail.Data.Changes) != 1 {
		t.Errorf("detail changes: got %d, want 1", len(detail.Data.Changes))
	}

	// Unknown id: 404.
	missingW := auditServeGet(t, logPath, true, base+"/deadbeefnonexistent")
	if missingW.Code != http.StatusNotFound {
		t.Errorf("unknown id status: got %d, want 404; body: %s", missingW.Code, missingW.Body.String())
	}
}

// TestAuditEvents_Order verifies the HTTP layer defaults to newest-first,
// honors ?order=asc (chronological) and ?order=desc, and rejects a bad
// value with 400 (fail-closed param validation).
func TestAuditEvents_Order(t *testing.T) {
	logPath := writeAuditLog(t, []audit.Event{
		{User: "alice", Operation: "op1", Success: true},
		{User: "alice", Operation: "op2", Success: true},
		{User: "alice", Operation: "op3", Success: true},
	})
	base := "/newtron/v1/networks/default/audit/events"

	firstOp := func(path string) string {
		w := auditServeGet(t, logPath, true, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d; body: %s", path, w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				Events []map[string]any `json:"events"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(env.Data.Events) == 0 {
			t.Fatalf("GET %s: no events", path)
		}
		op, _ := env.Data.Events[0]["operation"].(string)
		return op
	}

	if op := firstOp(base); op != "op3" {
		t.Errorf("default: first event = %q, want op3 (newest first)", op)
	}
	if op := firstOp(base + "?order=asc"); op != "op1" {
		t.Errorf("order=asc: first event = %q, want op1 (oldest first)", op)
	}
	if op := firstOp(base + "?order=desc"); op != "op3" {
		t.Errorf("order=desc: first event = %q, want op3 (newest first)", op)
	}
	if w := auditServeGet(t, logPath, true, base+"?order=sideways"); w.Code != http.StatusBadRequest {
		t.Errorf("order=sideways: status = %d, want 400", w.Code)
	}
}

// TestAuditEvents_FilterByUser pins the user-filter dimension —
// the Filter shape's primary value: "show me alice's actions".
// Verifies the query-string parameter reaches audit.Filter and the
// returned page omits non-matching events.
func TestAuditEvents_FilterByUser(t *testing.T) {
	logPath := writeAuditLog(t, []audit.Event{
		{User: "alice", Operation: "op1", Success: true},
		{User: "bob", Operation: "op2", Success: true},
		{User: "alice", Operation: "op3", Success: true},
	})
	w := auditServeGet(t, logPath, true, "/newtron/v1/networks/default/audit/events?user=alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Events []map[string]any `json:"events"`
			Total  int              `json:"total"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &envelope)
	if envelope.Data.Total != 2 {
		t.Errorf("total filtered by user=alice: got %d, want 2", envelope.Data.Total)
	}
	for _, ev := range envelope.Data.Events {
		if ev["user"] != "alice" {
			t.Errorf("non-alice event leaked through filter: %v", ev)
		}
	}
}

// TestAuditEvents_Paging pins that next_offset is non-nil when the
// page didn't exhaust the filter. limit=2 against a 5-entry log
// returns 2 events + total=5 + next_offset=2.
func TestAuditEvents_Paging(t *testing.T) {
	events := make([]audit.Event, 5)
	for i := range events {
		events[i] = audit.Event{User: "alice", Operation: "op", Success: true}
	}
	logPath := writeAuditLog(t, events)
	w := auditServeGet(t, logPath, true, "/newtron/v1/networks/default/audit/events?limit=2")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Events     []map[string]any `json:"events"`
			Total      int              `json:"total"`
			NextOffset *int             `json:"next_offset"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &envelope)
	if len(envelope.Data.Events) != 2 {
		t.Errorf("page len: got %d, want 2", len(envelope.Data.Events))
	}
	if envelope.Data.Total != 5 {
		t.Errorf("total: got %d, want 5", envelope.Data.Total)
	}
	if envelope.Data.NextOffset == nil || *envelope.Data.NextOffset != 2 {
		t.Errorf("next_offset: got %v, want 2", envelope.Data.NextOffset)
	}
}

// TestAuditEvents_BadFilterParse pins the 400 path: malformed
// timestamp surfaces an actionable error rather than silent
// fall-through. §16 — the actionable phrase ("expected RFC3339
// timestamp") is part of the contract.
func TestAuditEvents_BadFilterParse(t *testing.T) {
	logPath := writeAuditLog(t, []audit.Event{{User: "alice"}})
	w := auditServeGet(t, logPath, true, "/newtron/v1/networks/default/audit/events?since=not-a-date")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "RFC3339") {
		t.Errorf("body should contain the actionable phrase RFC3339; got: %s", w.Body.String())
	}
}

// TestAuditIntegrity_CleanChain returns the chain head + entry
// count + zero BreakAt + empty BreakReason for an integrity-
// chained log written without tamper.
func TestAuditIntegrity_CleanChain(t *testing.T) {
	logPath := writeAuditLog(t, []audit.Event{
		{User: "alice", Operation: "op1", Success: true},
		{User: "alice", Operation: "op2", Success: true},
		{User: "alice", Operation: "op3", Success: true},
	})
	w := auditServeGet(t, logPath, true, "/newtron/v1/networks/default/audit/integrity")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			ChainHeadHash string `json:"chain_head_hash"`
			EntryCount    int    `json:"entry_count"`
			BreakAt       int    `json:"break_at"`
			BreakReason   string `json:"break_reason"`
			VerifiedAt    string `json:"verified_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v; body: %s", err, w.Body.String())
	}
	got := envelope.Data
	if got.EntryCount != 3 {
		t.Errorf("entry_count: got %d, want 3", got.EntryCount)
	}
	if got.BreakAt != 0 {
		t.Errorf("break_at: got %d, want 0 (clean chain)", got.BreakAt)
	}
	if got.BreakReason != "" {
		t.Errorf("break_reason: got %q, want \"\" (clean chain)", got.BreakReason)
	}
	if got.ChainHeadHash == "" {
		t.Errorf("chain_head_hash: empty; want non-empty for clean chain")
	}
	if got.VerifiedAt == "" {
		t.Errorf("verified_at: empty; want server-side timestamp")
	}
}

// TestAuditIntegrity_TamperedChain pins detection: rewrite an
// entry's body in-place and verify break_at + break_reason
// populate. This is the operational tripwire test — if the hash
// chain ever stops detecting tampering, this test fails loud.
func TestAuditIntegrity_TamperedChain(t *testing.T) {
	dir := writeAuditLog(t, []audit.Event{
		{User: "alice", Operation: "op1", Success: true},
		{User: "alice", Operation: "op2", Success: true},
	})
	// Read, tamper with line 2, write back — the audit log lives at
	// audit.Path(dir) inside the network folder.
	logFile := audit.Path(dir)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tampered := strings.Replace(string(data), `"user":"alice"`, `"user":"attacker"`, 1)
	if tampered == string(data) {
		t.Fatal("test fixture broken: no substring to tamper")
	}
	if err := os.WriteFile(logFile, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	w := auditServeGet(t, dir, true, "/newtron/v1/networks/default/audit/integrity")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			BreakAt     int    `json:"break_at"`
			BreakReason string `json:"break_reason"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &envelope)
	if envelope.Data.BreakAt == 0 {
		t.Errorf("break_at: got 0 (chain reported clean) for a tampered log; want non-zero")
	}
	if envelope.Data.BreakReason == "" {
		t.Errorf("break_reason: empty for a tampered log; want non-empty (id mismatch or prev_hash mismatch)")
	}
}

// TestAuditEvents_EngageWhenConfigured_GateDenies pins the
// PermAuditRead gate: with EnforceAuthorization + an audit.read
// grant scoped to iam-team, mallory (no group) is denied; iam-ian
// is allowed; root super-bypasses.
func TestAuditEvents_EngageWhenConfigured_GateDenies(t *testing.T) {
	dir := t.TempDir()
	if err := spec.CreateEmpty(dir, "audit.read engage test"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	netJSON := `{
  "version": "1.0",
  "super_users": ["root"],
  "user_groups": {"iam-team": ["iam-ian"]},
  "permissions": {"audit.read": ["iam-team"]},
  "services": {}
}`
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(netJSON), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	writeZoneFile(t, dir, "amer")
	seedAuditLog(t, dir, []audit.Event{{User: "alice", Operation: "op", Success: true}})

	s := NewServer(Config{
		AuditCallerHeader:    "X-Newtron-Caller",
		Audit:                true,
		EnforceAuthorization: true,
	})
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	cases := []struct {
		caller   string
		wantCode int
		why      string
	}{
		{"mallory", http.StatusForbidden, "no group → no audit.read grant matches"},
		{"iam-ian", http.StatusOK, "iam-team granted audit.read"},
		{"root", http.StatusOK, "super_user bypass"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/audit/events", nil)
		req.Header.Set("X-Newtron-Caller", tc.caller)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.wantCode {
			t.Errorf("[%s] %s: got %d, want %d; body: %s", tc.caller, tc.why, w.Code, tc.wantCode, w.Body.String())
		}
	}
}

// TestAuditEvents_EngageWhenConfigured_Fallback pins the
// engage-when-configured contract: with EnforceAuthorization=true
// but NO audit.read entry in network.json, the endpoint stays
// ungated. mallory (denied by any actual gate) still gets 200.
// This preserves the legacy reachability that the CLI's
// `bin/newtron audit list` enjoys against a no-flag-set server.
func TestAuditEvents_EngageWhenConfigured_Fallback(t *testing.T) {
	dir := scaffoldAuditNetwork(t)
	seedAuditLog(t, dir, []audit.Event{{User: "alice", Operation: "op", Success: true}})
	s := NewServer(Config{
		AuditCallerHeader:    "X-Newtron-Caller",
		Audit:                true,
		EnforceAuthorization: true,
	})
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/audit/events", nil)
	req.Header.Set("X-Newtron-Caller", "mallory")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 (no audit.read entry → fallback ungated); body: %s", w.Code, w.Body.String())
	}
}
