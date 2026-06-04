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

func TestLabStatus_5xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "internal error"}`))
	}))
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.LabStatus(context.Background(), "anything")
	if err == nil {
		t.Fatal("LabStatus: expected error on 5xx")
	}
	se, ok := err.(*ServerError)
	if !ok {
		t.Fatalf("err = %T, want *ServerError", err)
	}
	if se.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", se.StatusCode)
	}
}

// TestSSHPort_TopologyNotDeployed exercises the typed-error path: when
// LabStatus 404s, PortResolver.SSHPort returns *NotInTopologyError so
// callers like newtron's /status endpoint can dispatch on the error class
// instead of substring-matching the message.
func TestSSHPort_TopologyNotDeployed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "topology not deployed"})
	}))
	defer ts.Close()

	r := NewPortResolver(New(ts.URL))
	_, err := r.SSHPort(context.Background(), "ghost-lab", "switch1")
	if err == nil {
		t.Fatal("SSHPort: expected error, got nil")
	}
	nit, ok := err.(*NotInTopologyError)
	if !ok {
		t.Fatalf("err type = %T, want *NotInTopologyError", err)
	}
	if nit.Topology != "ghost-lab" {
		t.Errorf("Topology = %q, want %q", nit.Topology, "ghost-lab")
	}
	if nit.Device != "" {
		t.Errorf("Device = %q, want empty (404 means whole topology missing)", nit.Device)
	}
}

// TestSSHPort_DeviceNotInTopology exercises the second typed-error path:
// the topology is deployed but doesn't include the named device.
func TestSSHPort_DeviceNotInTopology(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := newtlab.LabState{
			Name:  "1node-vs",
			Nodes: map[string]*newtlab.NodeState{"switch1": {SSHPort: 13000}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": state})
	}))
	defer ts.Close()

	r := NewPortResolver(New(ts.URL))
	_, err := r.SSHPort(context.Background(), "1node-vs", "switch99")
	nit, ok := err.(*NotInTopologyError)
	if !ok {
		t.Fatalf("err type = %T, want *NotInTopologyError", err)
	}
	if nit.Topology != "1node-vs" || nit.Device != "switch99" {
		t.Errorf("error fields: got {%q, %q}, want {1node-vs, switch99}", nit.Topology, nit.Device)
	}
}
