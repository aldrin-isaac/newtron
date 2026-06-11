package newtrun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// TestStep_HeadersParsedFromYAML pins the wire shape: a scenario
// YAML carrying step-level headers: { X-Newtron-Caller: alice }
// decodes into Step.Headers with the literal key/value.
func TestStep_HeadersParsedFromYAML(t *testing.T) {
	yaml := []byte(`
name: hdr-parse
description: yaml-binding test
steps:
  - name: as-alice
    action: newtron
    url: /newtron/v1/networks/default
    headers:
      X-Newtron-Caller: alice
      X-Trace-ID: t-123
`)
	s, err := ParseScenarioBytes(yaml)
	if err != nil {
		t.Fatalf("ParseScenarioBytes: %v", err)
	}
	if len(s.Steps) != 1 {
		t.Fatalf("Steps = %d, want 1", len(s.Steps))
	}
	step := s.Steps[0]
	if got := step.Headers["X-Newtron-Caller"]; got != "alice" {
		t.Errorf("X-Newtron-Caller = %q, want alice", got)
	}
	if got := step.Headers["X-Trace-ID"]; got != "t-123" {
		t.Errorf("X-Trace-ID = %q, want t-123", got)
	}
}

// TestStep_HeadersSentOnTheWire pins the end-to-end contract: when
// step.Headers is populated, the runner's outbound HTTP request
// carries those headers verbatim. This is the auth-design.md L1
// caller-identity flow exercised at the test framework level — every
// auth-test scenario depends on this.
func TestStep_HeadersSentOnTheWire(t *testing.T) {
	var (
		mu  sync.Mutex
		got http.Header
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ok": true}})
	}))
	defer server.Close()

	// Bypass connectToServer (which calls ListNetworks against a
	// stub that returns only one envelope) and construct the client
	// directly. The test target is the header-on-the-wire path, not
	// the network registration handshake.
	r := NewRunner(t.TempDir())
	r.ServerURL = server.URL
	r.NetworkID = "default"
	r.Client = client.New(server.URL, "default")

	step := &Step{
		Name:   "as-alice",
		Action: ActionNewtron,
		URL:    "/newtron/v1/networks/default",
		Headers: map[string]string{
			"X-Newtron-Caller": "alice",
			"X-Trace-ID":       "t-456",
		},
	}
	exec := newtronExecutor{}
	out := exec.Execute(context.Background(), r, step)
	if out.Result.Status != StepStatusPassed {
		t.Fatalf("step failed: %s", out.Result.Message)
	}

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("server never received a request")
	}
	if v := got.Get("X-Newtron-Caller"); v != "alice" {
		t.Errorf("server saw X-Newtron-Caller = %q, want alice", v)
	}
	if v := got.Get("X-Trace-ID"); v != "t-456" {
		t.Errorf("server saw X-Trace-ID = %q, want t-456", v)
	}
}
