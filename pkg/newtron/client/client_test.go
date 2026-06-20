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

// register-network client surface — wire shape is just {id}; the
// server resolves the on-disk path from its --networks-base. Client
// methods take no dir parameter.

// TestRegisterNetwork_201Success pins the happy path — a fresh
// register against an empty server returns nil.
func TestRegisterNetwork_201Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"demo","dir":"/srv/networks/demo","nodes":[]}}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	if err := c.RegisterNetwork(); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
}

// TestRegisterNetwork_500ReturnsServerError pins the non-2xx path —
// the client surfaces a typed *ServerError so callers that switch on
// it keep working.
func TestRegisterNetwork_500ReturnsServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"loading network failed"}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	err := c.RegisterNetwork()
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

// TestScaffoldNetwork_ReturnsResolvedDir pins the operator-language
// contract: the client passes just a description (no path); the
// response carries the server-resolved on-disk dir so the caller can
// display "created at <path>" without re-fetching.
func TestScaffoldNetwork_ReturnsResolvedDir(t *testing.T) {
	const resolvedPath = "/srv/newtron/networks/demo"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		env := httputil.APIResponse{
			Data: api.NetworkInfo{
				ID:    "demo",
				Dir:   resolvedPath,
				Nodes: []string{},
			},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	info, err := c.ScaffoldNetwork("test description")
	if err != nil {
		t.Fatalf("ScaffoldNetwork: %v", err)
	}
	if info == nil {
		t.Fatal("info should not be nil on success")
	}
	if info.Dir != resolvedPath {
		t.Errorf("info.Dir = %q, want %q", info.Dir, resolvedPath)
	}
	if info.ID != "demo" {
		t.Errorf("info.ID = %q, want demo", info.ID)
	}
}
