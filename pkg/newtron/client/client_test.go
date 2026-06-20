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

// CreateNetwork is the single client verb for "ensure the network is
// registered." Wire shape is {id, description?}; the server resolves
// the on-disk path from its --networks-base. The CLI surface returns
// the resolved NetworkInfo so callers learn the path without
// re-fetching.

// TestCreateNetwork_201Success pins the first-call happy path —
// server returns 201 with NetworkInfo.
func TestCreateNetwork_201Success(t *testing.T) {
	const resolvedPath = "/srv/networks/demo"
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
	info, err := c.CreateNetwork("")
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	if info.Dir != resolvedPath {
		t.Errorf("info.Dir = %q, want %q", info.Dir, resolvedPath)
	}
}

// TestCreateNetwork_200Idempotent confirms that 200 OK (subsequent
// call against an existing slot) is treated as success — the same
// success path 201 takes.
func TestCreateNetwork_200Idempotent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		env := httputil.APIResponse{
			Data: api.NetworkInfo{ID: "demo", Dir: "/srv/networks/demo", Nodes: []string{}},
		}
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	info, err := c.CreateNetwork("")
	if err != nil {
		t.Fatalf("CreateNetwork on 200: %v", err)
	}
	if info == nil || info.ID != "demo" {
		t.Errorf("info = %+v, want id=demo", info)
	}
}

// TestCreateNetwork_500ReturnsServerError pins the non-2xx path —
// the client surfaces a typed *ServerError so callers that switch on
// it keep working.
func TestCreateNetwork_500ReturnsServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"loading network failed"}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "demo")
	_, err := c.CreateNetwork("")
	if err == nil {
		t.Fatal("CreateNetwork should fail on 500")
	}
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("err type = %T, want *ServerError", err)
	}
	if se.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", se.StatusCode)
	}
}
