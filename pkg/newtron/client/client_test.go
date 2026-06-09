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
// when the server returns 409 because the same id+spec_dir is already
// registered, the client recognizes it via envelope.Data and treats it as
// success.
func TestRegisterNetwork_SameSpecDirReturnsNil(t *testing.T) {
	specDir := "/path/to/specs"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		env := httputil.APIResponse{
			Error: "network 'demo' is already registered with spec_dir '" + specDir + "'",
			Data: api.AlreadyRegisteredErrorInfo{
				ID:              "demo",
				ExistingSpecDir: specDir,
			},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	if err := c.RegisterNetwork(specDir); err != nil {
		t.Fatalf("RegisterNetwork on matching spec_dir should be nil; got %v", err)
	}
}

// TestRegisterNetwork_DifferentSpecDirReturnsTypedError pins the conflict
// path — when the server returns 409 because the id is registered for a
// different spec_dir, the client surfaces a typed *AlreadyRegisteredError
// carrying both paths so the operator can decide what to do.
func TestRegisterNetwork_DifferentSpecDirReturnsTypedError(t *testing.T) {
	existing := "/owned/by/2node-vs/specs"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		env := httputil.APIResponse{
			Error: "network 'default' is already registered with spec_dir '" + existing + "'",
			Data: api.AlreadyRegisteredErrorInfo{
				ID:              "default",
				ExistingSpecDir: existing,
			},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "default")
	requested := "/want/to/register/2node-vs-service/specs"
	err := c.RegisterNetwork(requested)
	if err == nil {
		t.Fatal("RegisterNetwork on mismatched spec_dir should fail; got nil")
	}

	var typed *AlreadyRegisteredError
	if !errors.As(err, &typed) {
		t.Fatalf("err type = %T, want *AlreadyRegisteredError", err)
	}
	if typed.ID != "default" {
		t.Errorf("ID = %q, want default", typed.ID)
	}
	if typed.RequestedSpecDir != requested {
		t.Errorf("RequestedSpecDir = %q, want %q", typed.RequestedSpecDir, requested)
	}
	if typed.ExistingSpecDir != existing {
		t.Errorf("ExistingSpecDir = %q, want %q", typed.ExistingSpecDir, existing)
	}
}

// TestRegisterNetwork_409WithEmptyDataReturnsTypedError pins the robustness
// edge case the §17 audit caught: when the server returns 409 but the
// envelope has no Data payload (or unparseable Data), the client must not
// collapse to "ExistingSpecDir == specDir" → nil when both happen to be
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
	// Request also has empty specDir — without the dataParsed guard this
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
