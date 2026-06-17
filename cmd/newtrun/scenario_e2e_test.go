// E2E tests for the `newtrun` CLI's suite + scenario subcommands.
//
// These exercise the full path the operator would walk: subprocess CLI
// → HTTP → real api.Server → on-disk filesystem. Verifies that the
// CLI's argv shape, client URL construction, and the server's on-disk
// persistence all line up — gaps that pure-go tests (handler-against-
// httptest, CLI argv parse) would miss.
//
// HTTP-level coverage lives in pkg/newtrun/api/scenarios_test.go.
// API-handler-against-httptest coverage covers the happy path of
// each handler; this layer covers the subprocess CLI on top.

package main

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

// buildCLI compiles the `newtrun` binary against the current source
// into a temp directory and returns the path. Cached per-test (one
// build per t.Run) so the subprocess launch path doesn't pull in the
// full go build cost on every assertion.
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
// returns the httptest.Server plus the topologies base directory the
// server is configured against. Tests that create suites via the CLI
// (with --network X) then assert on-disk state at
// suiteDirIn(networksBase, X, suiteName).
//
// Callers access ts.URL when sending the URL to a subprocess and ts
// directly when sharing the server across goroutines.
func newE2EServer(t *testing.T) (ts *httptest.Server, networksBase string) {
	t.Helper()
	networksBase = filepath.Join(t.TempDir(), "topologies")
	if err := os.MkdirAll(networksBase, 0755); err != nil {
		t.Fatalf("mkdir topologies base: %v", err)
	}
	srv := api.NewServer(api.Config{
		NetworksBase: networksBase,
		Logger:         log.New(io.Discard, "", 0),
	})
	ts = httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, networksBase
}

// suiteDirIn returns the on-disk path the production handler would
// write a suite to given its declared topology. Tests compose this
// when asserting on-disk state after `suite create --network X`.
func suiteDirIn(networksBase, topology, suite string) string {
	return filepath.Join(networksBase, topology, "suites", suite)
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
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestE2E_ScenarioLifecycle walks the full happy path an operator
// running curl-equivalent commands would do: create suite, put
// scenario, list, get, delete scenario, delete suite. Asserts every
// stage's side effect on disk so a regression in the CLI argv shaping
// or the client's URL construction surfaces here.
func TestE2E_ScenarioLifecycle(t *testing.T) {
	binPath := buildCLI(t)
	ts, networksBase := newE2EServer(t)

	const suite = "e2edemo"
	const network = "synthetic"
	const scenario = "smoke"
	suiteDir := suiteDirIn(networksBase, network, suite)
	body := []byte(`name: smoke
description: e2e smoke test
steps:
  - name: wait-one
    action: wait
    duration: 1s
`)

	// suite create
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "create", suite, "--network", network); rc != 0 {
		t.Fatalf("suite create exit=%d", rc)
	}
	if _, err := os.Stat(suiteDir); err != nil {
		t.Fatalf("suite dir not created: %v", err)
	}

	// scenario put (stdin)
	if _, _, rc := runCLI(t, binPath, ts.URL, body, "scenario", "put", suite, scenario); rc != 0 {
		t.Fatalf("scenario put exit=%d", rc)
	}
	got, err := os.ReadFile(filepath.Join(suiteDir, scenario+".yaml"))
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
	if _, err := os.Stat(filepath.Join(suiteDir, scenario+".yaml")); !os.IsNotExist(err) {
		t.Errorf("scenario file still present after delete: err=%v", err)
	}

	// suite delete
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "delete", suite); rc != 0 {
		t.Fatalf("suite delete exit=%d", rc)
	}
	if _, err := os.Stat(suiteDir); !os.IsNotExist(err) {
		t.Errorf("suite dir still present after delete: err=%v", err)
	}
}

// TestE2E_ScenarioPutFromFile exercises the --file flag path of
// `newtrun scenario put`. TestE2E_ScenarioLifecycle covers the stdin
// path; this test covers the file path so a regression in
// readScenarioBody's flag handling can't ship undetected.
func TestE2E_ScenarioPutFromFile(t *testing.T) {
	binPath := buildCLI(t)
	ts, networksBase := newE2EServer(t)
	const suite = "filedemo"
	const network = "synthetic"
	suiteDir := suiteDirIn(networksBase, network, suite)
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "create", suite, "--network", network); rc != 0 {
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
	got, err := os.ReadFile(filepath.Join(suiteDir, "from-file.yaml"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("on-disk body differs from --file body")
	}
}

// TestE2E_ScenarioPutRejectsBadYAML verifies that the validation
// gate surfaces all the way to the operator's exit code: bad YAML
// must NOT land on disk, the CLI must exit non-zero, and stderr
// must name the validation failure.
func TestE2E_ScenarioPutRejectsBadYAML(t *testing.T) {
	binPath := buildCLI(t)
	ts, networksBase := newE2EServer(t)
	const suite = "rejectdemo"
	const network = "synthetic"
	suiteDir := suiteDirIn(networksBase, network, suite)
	if _, _, rc := runCLI(t, binPath, ts.URL, nil, "suite", "create", suite, "--network", network); rc != 0 {
		t.Fatalf("suite create exit=%d", rc)
	}
	bad := []byte("not: a valid: scenario yaml: at all\n")
	_, stderr, rc := runCLI(t, binPath, ts.URL, bad, "scenario", "put", suite, "broken")
	if rc == 0 {
		t.Errorf("exit code: got 0, want non-zero for bad YAML")
	}
	if !strings.Contains(stderr, "invalid") && !strings.Contains(stderr, "parse") {
		t.Errorf("stderr: got %q, want substring describing the parse error", stderr)
	}
	// Critical: bad YAML must NOT land on disk.
	if _, err := os.Stat(filepath.Join(suiteDir, "broken.yaml")); !os.IsNotExist(err) {
		t.Errorf("broken.yaml landed on disk despite validation failure: err=%v", err)
	}
}
