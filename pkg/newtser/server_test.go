package newtser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
)

// silentLogger writes nothing — used to keep test output clean.
func silentLogger() *log.Logger { return log.New(&strings.Builder{}, "", 0) }

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	srv := NewServer(Config{Logger: silentLogger()})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestHealthEndpointReturnsOK(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/newtser/v1/health")
	if err != nil {
		t.Fatalf("GET /newtser/v1/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestListServicesEmptyOnFreshServer(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/newtser/v1/services")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var env httputil.APIResponse
	json.Unmarshal(body, &env)
	if items, ok := env.Data.([]any); !ok || len(items) != 0 {
		t.Errorf("Data = %v, want empty list", env.Data)
	}
}

func TestRegisterAddsServiceToRegistry(t *testing.T) {
	srv, ts := newTestServer(t)

	body, _ := json.Marshal(RegisterRequest{
		Name: "newtron", Version: "v1", Upstream: "http://127.0.0.1:19080",
	})
	resp, err := ts.Client().Post(ts.URL+"/newtser/v1/services", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	if got := srv.Registry().Get("newtron"); got == nil {
		t.Fatal("Registry().Get returned nil after register")
	}
}

func TestRegisterRejectsInvalidName(t *testing.T) {
	_, ts := newTestServer(t)
	body, _ := json.Marshal(RegisterRequest{
		Name: "bad/name", Version: "v1", Upstream: "http://x",
	})
	resp, err := ts.Client().Post(ts.URL+"/newtser/v1/services", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRegisterRejectsReservedName(t *testing.T) {
	_, ts := newTestServer(t)
	body, _ := json.Marshal(RegisterRequest{
		Name: "newtser", Version: "v1", Upstream: "http://x",
	})
	resp, err := ts.Client().Post(ts.URL+"/newtser/v1/services", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (reserved name)", resp.StatusCode)
	}
}

func TestDeregisterRemovesService(t *testing.T) {
	srv, ts := newTestServer(t)
	srv.Registry().Register("x", "v1", "http://a")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/newtser/v1/services/x", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if srv.Registry().Get("x") != nil {
		t.Error("service still in registry after DELETE")
	}
}

func TestProxyForwardsToRegisteredBackend(t *testing.T) {
	// Backend records the request it received.
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprintln(w, "hi from backend")
	}))
	defer backend.Close()

	srv, ts := newTestServer(t)
	srv.Registry().Register("backend", "v1", backend.URL)

	resp, err := ts.Client().Get(ts.URL + "/backend/v1/echo/test")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hi from backend\n" {
		t.Errorf("body = %q, want backend response", string(body))
	}
	if gotPath != "/backend/v1/echo/test" {
		t.Errorf("backend got path = %q, want full /backend/v1/echo/test", gotPath)
	}
}

func TestProxyReturns503ForUnknownService(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/nobody/v1/anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHeartbeatRefreshesLastSeen(t *testing.T) {
	srv, ts := newTestServer(t)
	srv.Registry().Register("x", "v1", "http://a")
	before := srv.Registry().Get("x").LastSeen

	resp, err := ts.Client().Post(ts.URL+"/newtser/v1/services/x/heartbeat", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	after := srv.Registry().Get("x").LastSeen
	if !after.After(before) {
		t.Error("LastSeen did not advance")
	}
}

func TestHeartbeatReturns404ForUnknown(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := ts.Client().Post(ts.URL+"/newtser/v1/services/x/heartbeat", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (re-register signal)", resp.StatusCode)
	}
}

func TestRegistrationClientEndToEnd(t *testing.T) {
	// Spin up newtser, register via the client helper, verify the
	// registry sees it, Close, verify it's gone.
	srv, ts := newTestServer(t)

	reg := Register(context.Background(), Registration{
		URL:               ts.URL,
		Name:              "demo",
		Version:           "v1",
		Upstream:          "http://127.0.0.1:99999",
		Logger:            silentLogger(),
		HeartbeatInterval: 30 * 60 * 1000, // long — we don't need a heartbeat in this test
	})

	// Wait briefly for the registration to land.
	deadlineCheck(t, func() bool {
		return srv.Registry().Get("demo") != nil
	}, "Registration did not register within timeout")

	reg.Close()

	deadlineCheck(t, func() bool {
		return srv.Registry().Get("demo") == nil
	}, "Registration.Close did not deregister within timeout")
}

func deadlineCheck(t *testing.T, ok func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok() {
		t.Fatal(msg)
	}
}
