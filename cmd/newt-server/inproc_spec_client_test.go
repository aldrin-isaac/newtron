package main

import (
	"path/filepath"
	"runtime"
	"testing"

	newtronapi "github.com/aldrin-isaac/newtron/pkg/newtron/api"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestInprocSpecClient_ReadsRegisteredNetwork verifies the adapter forwards
// each SpecClient method to the underlying *newtron.Network resolved by
// netID from the api.Server registry.
func TestInprocSpecClient_ReadsRegisteredNetwork(t *testing.T) {
	srv := newtronapi.NewServer(nil, 0, nil)
	specDir := filepath.Join(repoRoot(t), "newtrun", "topologies", "1node-vs", "specs")
	if err := srv.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}

	c := &inprocSpecClient{server: srv, netID: "default"}

	topo, err := c.GetTopology()
	if err != nil {
		t.Fatalf("GetTopology: %v", err)
	}
	if topo == nil {
		t.Fatal("GetTopology returned nil")
	}
	if _, ok := topo.Devices["switch1"]; !ok {
		t.Errorf("topology missing switch1; devices: %v", topo.Devices)
	}

	platforms, err := c.ListPlatforms()
	if err != nil {
		t.Fatalf("ListPlatforms: %v", err)
	}
	if platforms == nil || len(platforms.Platforms) == 0 {
		t.Errorf("ListPlatforms returned empty")
	}

	profile, err := c.ShowProfile("switch1")
	if err != nil {
		t.Fatalf("ShowProfile switch1: %v", err)
	}
	if profile == nil {
		t.Fatal("ShowProfile returned nil")
	}
}

// TestInprocSpecClient_UnregisteredNetwork verifies the adapter surfaces a
// clear error when the netID has nothing registered — mirrors the 404 the
// HTTP variant returns.
func TestInprocSpecClient_UnregisteredNetwork(t *testing.T) {
	srv := newtronapi.NewServer(nil, 0, nil)
	c := &inprocSpecClient{server: srv, netID: "missing"}

	if _, err := c.GetTopology(); err == nil {
		t.Errorf("GetTopology against missing netID returned nil error")
	}
	if _, err := c.ListPlatforms(); err == nil {
		t.Errorf("ListPlatforms against missing netID returned nil error")
	}
	if _, err := c.ShowProfile("anything"); err == nil {
		t.Errorf("ShowProfile against missing netID returned nil error")
	}
}
