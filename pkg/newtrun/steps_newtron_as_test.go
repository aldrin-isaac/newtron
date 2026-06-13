package newtrun

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// TestNewtronExecutor_AsAttachesBearer pins the per-step
// impersonation contract: when step.As names a user, the runner
// reads UserSessions[user] and attaches Authorization: Bearer.
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
	}
	step := &Step{
		Action: ActionNewtron,
		Method: "POST",
		URL:    "/create-vlan",
		Params: map[string]any{"id": 100},
		As:     "mallory",
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
// fail-fast contract: a step that names a user without a cached
// session in UserSessions returns a clear error mentioning the
// `newtron auth login` remediation. Operators see the missing
// identity at the first affected step, not after the suite
// completed silently misbehaving.
func TestNewtronExecutor_AsMissingSessionFailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server received a request despite missing-session error")
	}))
	defer srv.Close()

	r := &Runner{
		Client:       client.New(srv.URL, "net-1"),
		UserSessions: map[string]string{}, // empty
	}
	step := &Step{
		Action: ActionNewtron,
		Method: "POST",
		URL:    "/create-vlan",
		As:     "mallory",
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
// flow (auth-design.md §L2c "Identity forwarding through
// engines"). A runner whose newtron client was built with
// WithBearer("operator-key") — the production setup after
// pkg/newtrun/api/runs.go assigns the inbound request's Bearer
// to runner.OperatorBearer — must attach
// `Authorization: Bearer operator-key` on a step that doesn't
// specify `as:`. This is the default-credential layer; the
// per-step override layer is tested separately above.
func TestNewtronExecutor_NoAsForwardsOperatorBearer(t *testing.T) {
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
		Client: client.New(srv.URL, "net-1", client.WithBearer("operator-key")),
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
