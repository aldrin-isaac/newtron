package main

// CLI→server E2E coverage for the scenario CRUD surface, per
// ai-instructions §21 (Every API endpoint and operation must be
// exercised in at least one E2E test). The unit tests in
// pkg/newtrun/api/scenarios_test.go cover the handler-side; this
// drives the same endpoints through the actual bin/newtrun binary so
// cobra wiring, flag parsing, and the client's request shaping are
// also exercised. A regression that breaks any link in the
// CLI→client→HTTP chain shows up here, not after merge.

import (
	"bytes"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

// buildCLI compiles bin/newtrun-e2e once and returns its path. Tests
// share the binary to avoid rebuilding on every t.Run.
func buildCLI(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "newtrun-e2e")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binPath
}

// newE2EServer wires the real api.Server into an httptest.Server and
// returns the httptest.Server and the suites base directory.
//
// Return shape matches newScenarioTestServer in pkg/newtrun/api per
// §13 (Same Concept = Same Name): both helpers do the same thing
// (build a test server backed by a temp suites directory), so they
// expose the same return tuple. Callers access ts.URL when sending
// the URL to a subprocess and ts directly when sharing the server
// across goroutines.
func newE2EServer(t *testing.T) (ts *httptest.Server, suitesBase string) {
	t.Helper()
	suitesBase = filepath.Join(t.TempDir(), "suites")
	if err := os.MkdirAll(suitesBase, 0755); err != nil {
		t.Fatalf("mkdir suites: %v", err)
	}
	srv := api.NewServer(api.Config{
		SuitesBase:     suitesBase,
		TopologiesBase: filepath.Join(t.TempDir(), "topologies"),
		Logger:         log.New(io.Discard, "", 0),
	})
	ts = httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, suitesBase
}

// runCLI invokes the test binary with stdin/stdout/stderr captured.
// NEWTRUN_SERVER points at the httptest URL so the CLI's newClient()
// addresses the real handler.
func runCLI(t *testing.T, binPath, serverURL string, stdin []byte, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "NEWTRUN_SERVER="+serverURL)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestE2E_ScenarioLifecycle exercises the full suite → scenario CRUD
// round trip through the CLI. Mirrors what an operator (or newtcon
// running curl-equivalent commands) would do: create suite, put
// scenario, list, get, delete scenario, delete suite. Asserts every
// stage's side effect on disk so a regression in the CLI argv shaping
// or the client's URL construction surfaces here.
func TestE2E_ScenarioLifecycle(t *testing.T) {
	binPath := buildCLI(t)
	ts, suitesBase := newE2EServer(t)

	const suite = "e2edemo"
	const scenario = "smoke"
	body := []byte(`name: smoke
description: e2e smoke test
steps:
  - name: wait-one
    action: wait
    duration: 1s
`)

	// suite create
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "create", suite, "--topology", "synthetic"); rc != 0 {
		t.Fatalf("suite create exit=%d", rc)
	}
	if _, err := os.Stat(filepath.Join(suitesBase, suite)); err != nil {
		t.Fatalf("suite dir not created: %v", err)
	}

	// scenario put (stdin)
	if _, _, rc := runCLI(t, binPath, ts.URL, body, "scenario", "put", suite, scenario); rc != 0 {
		t.Fatalf("scenario put exit=%d", rc)
	}
	got, err := os.ReadFile(filepath.Join(suitesBase, suite, scenario+".yaml"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("on-disk body differs from PUT body")
	}

	// scenario list
	stdout, _, rc := runCLI(t, binPath, ts.URL, nil, "scenario", "list", suite)
	if rc != 0 {
		t.Fatalf("scenario list exit=%d", rc)
	}
	if !strings.Contains(stdout, scenario) {
		t.Errorf("list stdout missing scenario name; got: %q", stdout)
	}

	// scenario get
	stdout, _, rc = runCLI(t, binPath, ts.URL, nil, "scenario", "get", suite, scenario)
	if rc != 0 {
		t.Fatalf("scenario get exit=%d", rc)
	}
	if !bytes.Equal([]byte(stdout), body) {
		t.Errorf("get stdout differs from body")
	}

	// scenario delete
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "scenario", "delete", suite, scenario); rc != 0 {
		t.Fatalf("scenario delete exit=%d", rc)
	}
	if _, err := os.Stat(filepath.Join(suitesBase, suite, scenario+".yaml")); !os.IsNotExist(err) {
		t.Errorf("scenario file still present after delete: err=%v", err)
	}

	// suite delete
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "delete", suite); rc != 0 {
		t.Fatalf("suite delete exit=%d", rc)
	}
	if _, err := os.Stat(filepath.Join(suitesBase, suite)); !os.IsNotExist(err) {
		t.Errorf("suite dir still present after delete: err=%v", err)
	}
}

// TestE2E_ScenarioPutFromFile exercises the --file flag path of
// `newtrun scenario put`. TestE2E_ScenarioLifecycle covers the stdin
// path; this test covers the file path so a regression in
// readScenarioBody's flag handling can't ship undetected.
func TestE2E_ScenarioPutFromFile(t *testing.T) {
	binPath := buildCLI(t)
	ts, suitesBase := newE2EServer(t)
	const suite = "filedemo"
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "create", suite, "--topology", "synthetic"); rc != 0 {
		t.Fatalf("suite create exit=%d", rc)
	}
	body := []byte(`name: from-file
description: e2e test for --file path
steps:
  - name: wait
    action: wait
    duration: 1s
`)
	bodyPath := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(bodyPath, body, 0644); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if _, _, rc := runCLI(t, binPath, ts.URL, nil,
		"scenario", "put", suite, "from-file", "--file", bodyPath); rc != 0 {
		t.Fatalf("scenario put --file exit=%d", rc)
	}
	got, err := os.ReadFile(filepath.Join(suitesBase, suite, "from-file.yaml"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("on-disk body differs from --file body")
	}
}

// TestE2E_ScenarioPutRejectsBadYAML verifies that the validation
// gate surfaces all the way to the operator's exit code: bad YAML
// from the CLI side produces non-zero exit and a server error
// message on stderr. Without this, a future refactor that swallows
// the error in the CLI layer would go unnoticed.
func TestE2E_ScenarioPutRejectsBadYAML(t *testing.T) {
	binPath := buildCLI(t)
	ts, _ := newE2EServer(t)
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "create", "badyaml", "--topology", "synthetic"); rc != 0 {
		t.Fatalf("suite create exit=%d", rc)
	}
	stdout, stderr, rc := runCLI(t, binPath, ts.URL,
		[]byte("not: valid yaml: : : :"),
		"scenario", "put", "badyaml", "anything")
	if rc == 0 {
		t.Fatalf("bad YAML PUT exited 0; want non-zero")
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "400") && !strings.Contains(combined, "invalid scenario YAML") {
		t.Errorf("expected 400 / invalid YAML message; got stdout=%q stderr=%q", stdout, stderr)
	}
}
