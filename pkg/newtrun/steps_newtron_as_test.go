package newtrun

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// TestNewtronExecutor_AsAttachesBearer pins the per-scenario
// impersonation contract: when scenario.As names a user, the
// runner reads UserSessions[user] and attaches the named user's
// Bearer on every outbound newtron call this scenario makes.
// The faux server records the header it sees so the test can
// confirm the right key landed on the wire.
func TestNewtronExecutor_AsAttachesBearer(t *testing.T) {
	var (
		mu      sync.Mutex
		gotAuth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()

	r := &Runner{
		Client:       client.New(srv.URL, "net-1"),
		UserSessions: map[string]string{"mallory": "mallory-bearer-key"},
		scenario:     &Scenario{Name: "as-mallory", As: "mallory"},
	}
	step := &Step{
		Action: ActionNewtron,
		Method: "POST",
		URL:    "/create-vlan",
		Params: map[string]any{"id": 100},
	}
	exec := &newtronExecutor{}
	output := exec.Execute(t.Context(), r, step)
	if output.Result.Status != StepStatusPassed {
		t.Fatalf("status = %v, message = %q", output.Result.Status, output.Result.Message)
	}
	mu.Lock()
	got := gotAuth
	mu.Unlock()
	if got != "Bearer mallory-bearer-key" {
		t.Errorf("Authorization header = %q, want Bearer mallory-bearer-key", got)
	}
}

// TestNewtronExecutor_AsMissingSessionFailsFast pins the
// fail-fast contract: a scenario that names a user without a
// cached session in UserSessions returns a clear error mentioning
// the `newtron auth login` remediation. Operators see the missing
// identity at the first affected scenario, not after the suite
// completed silently misbehaving.
func TestNewtronExecutor_AsMissingSessionFailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server received a request despite missing-session error")
	}))
	defer srv.Close()

	r := &Runner{
		Client:       client.New(srv.URL, "net-1"),
		UserSessions: map[string]string{}, // empty
		scenario:     &Scenario{Name: "as-mallory", As: "mallory"},
	}
	step := &Step{
		Action: ActionNewtron,
		Method: "POST",
		URL:    "/create-vlan",
	}
	exec := &newtronExecutor{}
	output := exec.Execute(t.Context(), r, step)
	if output.Result.Status != StepStatusFailed {
		t.Fatalf("status = %v, want FAILED", output.Result.Status)
	}
	if !strings.Contains(output.Result.Message, "newtron auth login") {
		t.Errorf("error %q should suggest `newtron auth login`", output.Result.Message)
	}
	if !strings.Contains(output.Result.Message, "mallory") {
		t.Errorf("error %q should name the missing user", output.Result.Message)
	}
}

// TestNewtronExecutor_NoAsLeavesClientCredential pins that
// steps WITHOUT an `as:` field don't get any synthetic
// Authorization header — the runner's outbound client uses
// whatever it was constructed with (no auth in this test).
func TestNewtronExecutor_NoAsLeavesClientCredential(t *testing.T) {
	var (
		mu      sync.Mutex
		gotAuth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()

	r := &Runner{
		Client:       client.New(srv.URL, "net-1"),
		UserSessions: map[string]string{"alice": "should-not-be-used"},
	}
	step := &Step{
		Action: ActionNewtron,
		Method: "POST",
		URL:    "/create-vlan",
		// No As — should not touch Authorization.
	}
	exec := &newtronExecutor{}
	output := exec.Execute(t.Context(), r, step)
	if output.Result.Status != StepStatusPassed {
		t.Fatalf("status = %v, message = %q", output.Result.Status, output.Result.Message)
	}
	mu.Lock()
	got := gotAuth
	mu.Unlock()
	if got != "" {
		t.Errorf("Authorization header = %q, want empty for step without as:", got)
	}
}

// TestNewtronExecutor_NoAsForwardsOperatorBearer pins the
// engine-composition refactor (PR C) operator-Bearer-forward
// flow end-to-end on the outbound side (auth-design.md §L2c
// "Identity forwarding through engines"). The full chain
// connectToServer reads Runner.OperatorBearer, hands it to
// client.WithBearer, and the resulting client attaches
// `Authorization: Bearer operator-key` on a step that doesn't
// specify `as:`. This is the default-credential layer; the
// per-step override layer is tested separately above. The
// inbound-side parse from the /runs request's Authorization
// header is covered by TestOperatorBearer_ExtractsFromAuthorization
// Header in the api package — those two together pin the chain
// from inbound request to outbound wire.
func TestNewtronExecutor_NoAsForwardsOperatorBearer(t *testing.T) {
	var (
		mu      sync.Mutex
		gotAuth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// connectToServer probes GET /networks (returns a list);
		// step calls land on POST /networks/<id>/<verb>. We only
		// need a valid wire shape on either path. We capture the
		// Authorization header on the POST (step call) since that's
		// the path the test is asserting about.
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":[{"id":"net-1","spec_dir":"/tmp","has_topology":false,"topology":"t","nodes":[]}]}`))
			return
		}
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()

	r := &Runner{
		ServerURL:      srv.URL,
		NetworkID:      "net-1",
		OperatorBearer: "operator-key",
	}
	// connectToServer is the production wire-up: it reads
	// r.OperatorBearer and constructs r.Client via client.WithBearer.
	// Exercise the same path the runs.go handler triggers.
	if err := r.connectToServer(); err != nil {
		t.Fatalf("connectToServer: %v", err)
	}
	step := &Step{
		Action: ActionNewtron,
		Method: "POST",
		URL:    "/create-vlan",
	}
	exec := &newtronExecutor{}
	output := exec.Execute(t.Context(), r, step)
	if output.Result.Status != StepStatusPassed {
		t.Fatalf("status = %v, message = %q", output.Result.Status, output.Result.Message)
	}
	mu.Lock()
	got := gotAuth
	mu.Unlock()
	if got != "Bearer operator-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer operator-key")
	}
}
