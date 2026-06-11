package newtrun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRun_CaptureEndToEnd is the integration test the user-visible
// capture feature would be incomplete without: it stands up a faux
// newtron-server, runs a two-step scenario where step 1 captures a
// value from the response body and step 2 references it in an
// outbound header, and asserts step 2's request actually carried
// the captured value.
//
// The faux server is patterned after run_integration_test.go's
// httptest stand-in: a /networks registry that satisfies the
// runner's topology guard, plus the two endpoints under test —
// /create-zone (which captures from) and /delete-zone (which reads
// the captured value back out of the Authorization header so the
// test can assert it).
func TestRun_CaptureEndToEnd(t *testing.T) {
	scenariosDir := t.TempDir()
	suiteYAML := `name: capture-int
description: integration suite for response-capture
topology: synthetic
`
	if err := os.WriteFile(filepath.Join(scenariosDir, "suite.yaml"), []byte(suiteYAML), 0o644); err != nil {
		t.Fatalf("write suite.yaml: %v", err)
	}
	// Step 1 captures `.name` from /create-zone's response. The
	// newtron client unwraps the {data, error} envelope before the
	// body reaches capture, matching what expect.jq sees today, so
	// the JQ expression starts at the data payload's top.
	// Step 2 references it as {{captured.zone_name}} in both the
	// Authorization header AND in the params body so we exercise
	// both substitution sites for captured values.
	scenarioYAML := `name: round-trip
description: capture from one response, reuse in the next
steps:
  - name: create
    action: newtron
    method: POST
    url: /create-zone
    params:
      name: integration-zone
    capture:
      zone_name: .name
  - name: delete-using-captured
    action: newtron
    method: POST
    url: /delete-zone
    params:
      name: "{{captured.zone_name}}"
    headers:
      Authorization: "Bearer {{captured.zone_name}}"
`
	if err := os.WriteFile(filepath.Join(scenariosDir, "00-rt.yaml"), []byte(scenarioYAML), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}

	var (
		mu                sync.Mutex
		deleteAuthHeader  string
		deleteParamsBody  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/newtron/v1/networks":
			_, _ = w.Write([]byte(`{"data":[{"id":"test-net","topology":"synthetic","has_topology":true,"spec_dir":""}]}`))
		case strings.HasSuffix(r.URL.Path, "/topology/devices"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/create-zone"):
			_, _ = w.Write([]byte(`{"data":{"name":"integration-zone"}}`))
		case strings.HasSuffix(r.URL.Path, "/delete-zone"):
			buf := make([]byte, 512)
			n, _ := r.Body.Read(buf)
			mu.Lock()
			deleteAuthHeader = r.Header.Get("Authorization")
			deleteParamsBody = string(buf[:n])
			mu.Unlock()
			_, _ = w.Write([]byte(`{"data":null}`))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()

	runner := NewRunner(scenariosDir)
	runner.ServerURL = srv.URL
	runner.NetworkID = "test-net"

	results, err := runner.Run(context.Background(), RunOptions{
		All:      true,
		NoDeploy: true,
		Keep:     true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 scenario", len(results))
	}
	if results[0].Status != StepStatusPassed {
		t.Fatalf("scenario status = %v, steps = %+v", results[0].Status, results[0].Steps)
	}

	// The /delete-zone request must have seen the captured zone
	// name in both the Authorization header (string-context
	// substitution) and in the params body (JSON-context typed
	// passthrough). If capture or expansion broke anywhere, the
	// header would contain the literal "{{captured.zone_name}}"
	// string instead of "integration-zone".
	mu.Lock()
	gotAuth := deleteAuthHeader
	gotBody := deleteParamsBody
	mu.Unlock()
	if gotAuth != "Bearer integration-zone" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer integration-zone")
	}
	if !strings.Contains(gotBody, `"name":"integration-zone"`) {
		t.Errorf("delete body = %q, want it to contain integration-zone", gotBody)
	}
}
