package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
)

// TestRegisterNetwork_201Success pins the happy path — a fresh register
// against an empty server returns nil.
func TestRegisterNetwork_201Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"demo"}}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	if err := c.RegisterNetwork("/path/to/specs"); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
}

// TestRegisterNetwork_SameSpecDirReturnsNil pins the true-idempotent path —
// when the server returns 409 because the same id+dir is already
// registered, the client recognizes it via envelope.Data and treats it as
// success.
func TestRegisterNetwork_SameSpecDirReturnsNil(t *testing.T) {
	dir := "/path/to/specs"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		env := httputil.APIResponse{
			Error: "network 'demo' is already registered with dir '" + dir + "'",
			Data: api.AlreadyRegisteredErrorInfo{
				ID:              "demo",
				ExistingDir: dir,
			},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	if err := c.RegisterNetwork(dir); err != nil {
		t.Fatalf("RegisterNetwork on matching dir should be nil; got %v", err)
	}
}

// TestRegisterNetwork_DifferentSpecDirReturnsTypedError pins the conflict
// path — when the server returns 409 because the id is registered for a
// different dir, the client surfaces a typed *AlreadyRegisteredError
// carrying both paths so the operator can decide what to do.
func TestRegisterNetwork_DifferentSpecDirReturnsTypedError(t *testing.T) {
	existing := "/owned/by/2node-vs/specs"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		env := httputil.APIResponse{
			Error: "network 'default' is already registered with dir '" + existing + "'",
			Data: api.AlreadyRegisteredErrorInfo{
				ID:              "default",
				ExistingDir: existing,
			},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "default")
	requested := "/want/to/register/2node-vs-service/specs"
	err := c.RegisterNetwork(requested)
	if err == nil {
		t.Fatal("RegisterNetwork on mismatched dir should fail; got nil")
	}

	var typed *AlreadyRegisteredError
	if !errors.As(err, &typed) {
		t.Fatalf("err type = %T, want *AlreadyRegisteredError", err)
	}
	if typed.ID != "default" {
		t.Errorf("ID = %q, want default", typed.ID)
	}
	if typed.RequestedDir != requested {
		t.Errorf("RequestedDir = %q, want %q", typed.RequestedDir, requested)
	}
	if typed.ExistingDir != existing {
		t.Errorf("ExistingDir = %q, want %q", typed.ExistingDir, existing)
	}
}

// TestRegisterNetwork_409WithEmptyDataReturnsTypedError pins the robustness
// edge case the §17 audit caught: when the server returns 409 but the
// envelope has no Data payload (or unparseable Data), the client must not
// collapse to "ExistingDir == dir" → nil when both happen to be
// empty strings. It returns *AlreadyRegisteredError so the caller can see
// the conflict surfaced.
func TestRegisterNetwork_409WithEmptyDataReturnsTypedError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		// Envelope with no Data — older servers, broken middleware, or
		// future error shapes the client doesn't recognize.
		_, _ = w.Write([]byte(`{"error":"network 'default' already registered"}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "default")
	// Request also has empty dir — without the dataParsed guard this
	// would degenerately match and silently succeed.
	err := c.RegisterNetwork("")
	if err == nil {
		t.Fatal("RegisterNetwork on 409-with-empty-Data should fail; got nil")
	}
	var typed *AlreadyRegisteredError
	if !errors.As(err, &typed) {
		t.Fatalf("err type = %T, want *AlreadyRegisteredError", err)
	}
}

// TestRegisterNetwork_500ReturnsServerError pins the non-409 error path —
// the client should still return *ServerError for any other failure so
// existing callers that switch on ServerError keep working.
func TestRegisterNetwork_500ReturnsServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"loading network from /missing: open /missing: no such file or directory"}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	err := c.RegisterNetwork("/missing")
	if err == nil {
		t.Fatal("RegisterNetwork should fail on 500")
	}
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("err type = %T, want *ServerError", err)
	}
	if se.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", se.StatusCode)
	}
}

// TestScaffoldNetwork_DerivedPath_ReturnsResolvedSpecDir pins the #122
// contract: the client passes an empty dir, the server picks the
// path under its scaffold root, and the response carries that resolved
// path back as NetworkInfo so the caller can display "created at <path>"
// without re-fetching.
func TestScaffoldNetwork_DerivedPath_ReturnsResolvedSpecDir(t *testing.T) {
	const resolvedPath = "/srv/newtron/topologies/demo-derived"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirror what the real handler returns on 201 — NetworkInfo
		// wrapped in the standard envelope.
		w.WriteHeader(http.StatusCreated)
		env := httputil.APIResponse{
			Data: api.NetworkInfo{
				ID:      "demo-derived",
				Dir: resolvedPath,
				Nodes:   []string{},
			},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "demo-derived")
	info, err := c.ScaffoldNetwork("", "test description")
	if err != nil {
		t.Fatalf("ScaffoldNetwork: %v", err)
	}
	if info == nil {
		t.Fatal("info should not be nil on success")
	}
	if info.Dir != resolvedPath {
		t.Errorf("info.Dir = %q, want %q", info.Dir, resolvedPath)
	}
	if info.ID != "demo-derived" {
		t.Errorf("info.ID = %q, want demo-derived", info.ID)
	}
}

// TestScaffoldNetwork_ExplicitPath_StillReturnsInfo pins that the
// uniform-response shape holds for the existing CLI workflow too — the
// client passes a path it picked, and gets the same NetworkInfo back
// (the server echoes the operator-supplied path under .Dir).
func TestScaffoldNetwork_ExplicitPath_StillReturnsInfo(t *testing.T) {
	const explicitPath = "/my/chosen/path"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		env := httputil.APIResponse{
			Data: api.NetworkInfo{
				ID:      "demo-explicit",
				Dir: explicitPath,
				Nodes:   []string{},
			},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "demo-explicit")
	info, err := c.ScaffoldNetwork(explicitPath, "")
	if err != nil {
		t.Fatalf("ScaffoldNetwork: %v", err)
	}
	if info.Dir != explicitPath {
		t.Errorf("info.Dir = %q, want %q", info.Dir, explicitPath)
	}
}
