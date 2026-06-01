package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

func TestLabStatus_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/newtlab/v1/topologies/2node-vs-service/status"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		state := newtlab.LabState{
			Name: "2node-vs-service",
			Nodes: map[string]*newtlab.NodeState{
				"switch1": {SSHPort: 13009, ConsolePort: 12009, Status: "running"},
				"switch2": {SSHPort: 13010, ConsolePort: 12010, Status: "running"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": state})
	}))
	defer ts.Close()

	c := New(ts.URL)
	got, err := c.LabStatus(context.Background(), "2node-vs-service")
	if err != nil {
		t.Fatalf("LabStatus: %v", err)
	}
	if got.Name != "2node-vs-service" {
		t.Errorf("Name = %q, want %q", got.Name, "2node-vs-service")
	}
	if p := got.Nodes["switch1"].SSHPort; p != 13009 {
		t.Errorf("switch1.SSHPort = %d, want 13009", p)
	}
	if p := got.Nodes["switch2"].SSHPort; p != 13010 {
		t.Errorf("switch2.SSHPort = %d, want 13010", p)
	}
}

func TestLabStatus_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "topology not deployed"})
	}))
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.LabStatus(context.Background(), "nonesuch")
	if err == nil {
		t.Fatal("LabStatus: expected error, got nil")
	}
	se, ok := err.(*ServerError)
	if !ok {
		t.Fatalf("err = %T, want *ServerError", err)
	}
	if se.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", se.StatusCode)
	}
}

func TestLabStatus_BadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": "not a LabState"}`))
	}))
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.LabStatus(context.Background(), "anything")
	if err == nil {
		t.Fatal("LabStatus: expected error decoding bad LabState")
	}
}

func TestLabStatus_EmptyBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
	}))
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.LabStatus(context.Background(), "anything")
	if err == nil {
		t.Fatal("LabStatus: expected error on empty body")
	}
}
