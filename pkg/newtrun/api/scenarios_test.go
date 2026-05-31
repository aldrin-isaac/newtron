package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildValidScenarioYAML is the minimum body ParseScenarioBytes accepts for
// the named scenario. Every test that needs a writable body builds on
// top of this. Keeping it as a single source of truth avoids per-test
// drift if the parser ever gets more strict.
func buildValidScenarioYAML(name string) []byte {
	return []byte(fmt.Sprintf(`name: %s
description: synthetic scenario for tests
topology: synthetic
platform: sonic-vs
steps:
  - name: wait-one
    action: wait
    duration: 1s
`, name))
}

// newScenarioTestServer wires the test Server into an httptest server
// and prepares one suite directory ready for scenario operations. All
// happy-path tests share this fixture; tests that exercise missing
// suites operate on a different name explicitly.
func newScenarioTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	t.Cleanup(ts.Close)
	suiteDir := filepath.Join(srv.cfg.SuitesBase, "demo")
	if err := os.MkdirAll(suiteDir, 0755); err != nil {
		t.Fatalf("mkdir suite: %v", err)
	}
	return ts, suiteDir
}

func doRequest(t *testing.T, ts *httptest.Server, method, path string, body []byte) (*http.Response, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if reader == nil {
		req, err = http.NewRequest(method, ts.URL+path, nil)
	} else {
		req, err = http.NewRequest(method, ts.URL+path, reader)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, respBody
}

// TestScenario_PutCreatesFile covers the create path: PUT to a name
// that doesn't yet exist returns 201 and lands a parseable YAML file at
// <suite>/<name>.yaml.
func TestScenario_PutCreatesFile(t *testing.T) {
	ts, suiteDir := newScenarioTestServer(t)
	body := buildValidScenarioYAML("hello")
	resp, _ := doRequest(t, ts, http.MethodPut, "/api/v1/suites/demo/scenarios/hello", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT status: got %d, want 201", resp.StatusCode)
	}
	path := filepath.Join(suiteDir, "hello.yaml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("on-disk body differs from PUT body")
	}
}

// TestScenario_PutUpdatesExisting covers the update path: a second PUT
// to the same name returns 200 (not 201) and overwrites the file
// in-place, preserving its on-disk name (including any "NN-" prefix).
func TestScenario_PutUpdatesExisting(t *testing.T) {
	ts, suiteDir := newScenarioTestServer(t)
	// Seed a pre-existing file with the lexical-prefix convention.
	prefixed := filepath.Join(suiteDir, "10-hello.yaml")
	if err := os.WriteFile(prefixed, buildValidScenarioYAML("hello"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	updated := append(buildValidScenarioYAML("hello"), []byte("# updated\n")...)
	resp, _ := doRequest(t, ts, http.MethodPut, "/api/v1/suites/demo/scenarios/hello", updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status: got %d, want 200", resp.StatusCode)
	}
	got, err := os.ReadFile(prefixed)
	if err != nil {
		t.Fatalf("read prefixed file: %v", err)
	}
	if !bytes.Equal(got, updated) {
		t.Errorf("update did not land at original prefixed path")
	}
	if _, err := os.Stat(filepath.Join(suiteDir, "hello.yaml")); err == nil {
		t.Errorf("update incorrectly created a second file at hello.yaml")
	}
}

// TestScenario_PutIsIdempotent covers the second-write contract: two
// identical PUTs against the same name both succeed. The first creates
// (201); the second updates (200) with the same body. Final on-disk
// content matches exactly. Catches a regression where the create path
// might inadvertently refuse re-application.
func TestScenario_PutIsIdempotent(t *testing.T) {
	ts, suiteDir := newScenarioTestServer(t)
	body := buildValidScenarioYAML("idempotent")

	resp1, _ := doRequest(t, ts, http.MethodPut, "/api/v1/suites/demo/scenarios/idempotent", body)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first PUT status: got %d, want 201", resp1.StatusCode)
	}
	resp2, _ := doRequest(t, ts, http.MethodPut, "/api/v1/suites/demo/scenarios/idempotent", body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second PUT status: got %d, want 200", resp2.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(suiteDir, "idempotent.yaml"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("file content drifted after repeated identical PUT")
	}
}

// TestScenario_PutRejectsBadYAML covers the validation gate: a body
// that fails ParseScenarioBytes returns 400 and never touches the
// suite directory.
func TestScenario_PutRejectsBadYAML(t *testing.T) {
	ts, suiteDir := newScenarioTestServer(t)
	resp, body := doRequest(t, ts, http.MethodPut, "/api/v1/suites/demo/scenarios/hello",
		[]byte("this is not valid yaml: : :"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT bad YAML status: got %d, want 400; body=%s", resp.StatusCode, body)
	}
	entries, _ := os.ReadDir(suiteDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml") {
			t.Errorf("rejected PUT left a .yaml file behind: %s", e.Name())
		}
	}
}

// TestScenario_PutRejectsNameMismatch covers the URL-vs-body
// consistency check: a body whose name: field disagrees with the URL
// name is rejected so the operator cannot accidentally clobber a
// different scenario via a misaddressed PUT.
func TestScenario_PutRejectsNameMismatch(t *testing.T) {
	ts, _ := newScenarioTestServer(t)
	resp, _ := doRequest(t, ts, http.MethodPut, "/api/v1/suites/demo/scenarios/hello",
		buildValidScenarioYAML("goodbye"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("name-mismatch PUT status: got %d, want 400", resp.StatusCode)
	}
}

// TestScenario_GetReturnsRawYAML covers the read path: GET returns the
// exact bytes that were written, with application/yaml Content-Type so
// browser-side consumers don't need to guess.
func TestScenario_GetReturnsRawYAML(t *testing.T) {
	ts, suiteDir := newScenarioTestServer(t)
	body := buildValidScenarioYAML("readback")
	if err := os.WriteFile(filepath.Join(suiteDir, "readback.yaml"), body, 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, got := doRequest(t, ts, http.MethodGet, "/api/v1/suites/demo/scenarios/readback", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type: got %q, want application/yaml", ct)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("GET body differs from on-disk content")
	}
}

func TestScenario_GetMissing(t *testing.T) {
	ts, _ := newScenarioTestServer(t)
	resp, _ := doRequest(t, ts, http.MethodGet, "/api/v1/suites/demo/scenarios/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing scenario GET: got %d, want 404", resp.StatusCode)
	}
}

func TestScenario_Delete(t *testing.T) {
	ts, suiteDir := newScenarioTestServer(t)
	path := filepath.Join(suiteDir, "doomed.yaml")
	if err := os.WriteFile(path, buildValidScenarioYAML("doomed"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, _ := doRequest(t, ts, http.MethodDelete, "/api/v1/suites/demo/scenarios/doomed", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status: got %d, want 204", resp.StatusCode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present after DELETE: err=%v", err)
	}
}

// TestScenario_ConcurrentPutsAreAtomic covers the temp-and-rename
// guarantee: N goroutines hammering PUT on the same name produce no
// partial files and leave the final file's contents matching one of
// the bodies that won the race (never a corruption).
func TestScenario_ConcurrentPutsAreAtomic(t *testing.T) {
	ts, suiteDir := newScenarioTestServer(t)
	const writers = 16
	bodies := make([][]byte, writers)
	for i := range bodies {
		bodies[i] = []byte(fmt.Sprintf(`name: race
description: writer-%d
topology: synthetic
platform: sonic-vs
steps:
  - name: wait
    action: wait
    duration: 1s
`, i))
	}
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, _ := doRequest(t, ts, http.MethodPut,
				"/api/v1/suites/demo/scenarios/race", bodies[idx])
			if resp.StatusCode >= 400 {
				t.Errorf("writer %d: status %d", idx, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()

	// Verify exactly one race.yaml exists and its contents match
	// exactly one of the writer bodies — no partials, no merge.
	got, err := os.ReadFile(filepath.Join(suiteDir, "race.yaml"))
	if err != nil {
		t.Fatalf("read final file: %v", err)
	}
	matched := false
	for _, b := range bodies {
		if bytes.Equal(got, b) {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("final file content matches none of the %d writers — partial write?", writers)
	}

	// Verify no tempfiles leaked.
	entries, _ := os.ReadDir(suiteDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".scenario-") {
			t.Errorf("tempfile leaked: %s", e.Name())
		}
	}
}

// TestSuite_CreateAndDelete covers the suite-level lifecycle: POST
// creates an empty directory, DELETE removes it, and DELETE on a
// non-empty suite returns 409 to surface the destructive intent
// explicitly.
func TestSuite_CreateAndDelete(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, _ := doRequest(t, ts, http.MethodPost, "/api/v1/suites",
		[]byte(`{"name":"fresh"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status: got %d, want 201", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.SuitesBase, "fresh")); err != nil {
		t.Errorf("suite dir not created: %v", err)
	}

	// Duplicate POST → 409.
	resp, _ = doRequest(t, ts, http.MethodPost, "/api/v1/suites",
		[]byte(`{"name":"fresh"}`))
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate POST status: got %d, want 409", resp.StatusCode)
	}

	// Put a scenario, try to delete, expect 409.
	doRequest(t, ts, http.MethodPut, "/api/v1/suites/fresh/scenarios/blocker",
		buildValidScenarioYAML("blocker"))
	resp, _ = doRequest(t, ts, http.MethodDelete, "/api/v1/suites/fresh", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("non-empty suite DELETE: got %d, want 409", resp.StatusCode)
	}

	// Delete the scenario, then delete the suite, expect 204.
	doRequest(t, ts, http.MethodDelete, "/api/v1/suites/fresh/scenarios/blocker", nil)
	resp, _ = doRequest(t, ts, http.MethodDelete, "/api/v1/suites/fresh", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("empty-suite DELETE: got %d, want 204", resp.StatusCode)
	}
}

// TestSuite_RejectsBadName covers the name-validation gate at suite
// granularity. Same regex protects scenario URLs from path traversal
// and shell-unsafe characters.
func TestSuite_RejectsBadName(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()
	for _, bad := range []string{"../escape", "with/slash", "", "dot.name"} {
		resp, _ := doRequest(t, ts, http.MethodPost, "/api/v1/suites",
			[]byte(fmt.Sprintf(`{"name":%q}`, bad)))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST name %q: got %d, want 400", bad, resp.StatusCode)
		}
	}
}

// TestScenario_RejectsBadName covers the scenario-level name guard.
// requireScenarioParams runs the same nameRE on both the suite and
// the scenario path segments; this asserts the scenario segment is
// actually checked (TestSuite_RejectsBadName only proves the suite
// segment is). Exercises GET, PUT, and DELETE so the guard fires on
// every path that takes a name.
//
// Each input is tested against its specific expected status code
// (not "any 4xx is OK"), distinguishing routing-layer rejection from
// handler-layer rejection. If a future change shifts the layer
// that catches a particular input — e.g., the handler starts
// accepting "dot.name" because nameRE drifted — the assertion fails
// even though the request would still be rejected somewhere else.
//
// Note: the 405 for ".." and 404 for "with/slash" come from Go
// stdlib net/http.ServeMux (Go 1.22+ method-aware routing + path
// cleaning). If a future Go release changes either rule, these
// expectations may shift even though our security boundary is
// unchanged. The "dot.name" and "-leading-dash" cases assert the
// handler-layer guard directly and are independent of stdlib
// changes.
func TestScenario_RejectsBadName(t *testing.T) {
	ts, _ := newScenarioTestServer(t)
	cases := []struct {
		input string
		want  int
		why   string
	}{
		{"..", http.StatusMethodNotAllowed, "path cleaned to /scenarios; list route is GET-only on all paths"},
		{"with/slash", http.StatusNotFound, "ServeMux {name} segment cannot contain a slash; path doesn't match any route"},
		{"dot.name", http.StatusBadRequest, "reaches handler; nameRE rejects dot"},
		{"-leading-dash", http.StatusBadRequest, "reaches handler; nameRE requires alphanumeric first char"},
	}
	for _, tc := range cases {
		path := "/api/v1/suites/demo/scenarios/" + tc.input
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			resp, _ := doRequest(t, ts, method, path, nil)
			if resp.StatusCode != tc.want {
				t.Errorf("%s %q (%s): got %d, want %d",
					method, tc.input, tc.why, resp.StatusCode, tc.want)
			}
		}
	}
}
