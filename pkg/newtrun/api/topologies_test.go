package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestCreateTopology_HappyPath covers the documented #76 flow: 201
// with {name, spec_dir} and the on-disk layout newtron's Loader
// expects (network.json, platforms.json, topology.json, profiles/).
func TestCreateTopology_HappyPath(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, body := doRequest(t, ts, http.MethodPost, "/newtrun/v1/topologies",
		[]byte(`{"name":"demo-1","description":"demo lab"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", resp.StatusCode, body)
	}

	var env httputil.APIResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data, _ := env.Data.(map[string]any)
	if got := data["name"]; got != "demo-1" {
		t.Errorf("name: got %v, want demo-1", got)
	}
	specDir, _ := data["spec_dir"].(string)
	want := filepath.Join(srv.cfg.TopologiesBase, "demo-1", "specs")
	if specDir != want {
		t.Errorf("spec_dir: got %q, want %q", specDir, want)
	}

	// On-disk layout: three spec files + empty profiles/ subdir.
	for _, f := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(specDir, f)); err != nil {
			t.Errorf("missing seed file %s: %v", f, err)
		}
	}
	profiles := filepath.Join(specDir, "profiles")
	info, err := os.Stat(profiles)
	if err != nil || !info.IsDir() {
		t.Errorf("profiles/ subdir: stat=%v isdir=%v", err, info != nil && info.IsDir())
	}

	// topology.json must parse back into a TopologySpecFile and carry
	// the description verbatim — that's the only field the operator
	// could supply at create time.
	topoBytes, err := os.ReadFile(filepath.Join(specDir, "topology.json"))
	if err != nil {
		t.Fatalf("read topology.json: %v", err)
	}
	var topo spec.TopologySpecFile
	if err := json.Unmarshal(topoBytes, &topo); err != nil {
		t.Fatalf("parse topology.json: %v", err)
	}
	if topo.Version != "1.0" {
		t.Errorf("topology.json version: got %q, want 1.0", topo.Version)
	}
	if topo.Description != "demo lab" {
		t.Errorf("topology.json description: got %q, want %q", topo.Description, "demo lab")
	}
}

// TestCreateTopology_Conflict ensures the second POST against the same
// name returns 409 and does NOT clobber the first directory's contents.
func TestCreateTopology_Conflict(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, _ := doRequest(t, ts, http.MethodPost, "/newtrun/v1/topologies",
		[]byte(`{"name":"demo-1"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first POST: got %d, want 201", resp.StatusCode)
	}
	// Stamp a sentinel file under the first directory.
	sentinel := filepath.Join(srv.cfg.TopologiesBase, "demo-1", "specs", "sentinel.json")
	if err := os.WriteFile(sentinel, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	resp, _ = doRequest(t, ts, http.MethodPost, "/newtrun/v1/topologies",
		[]byte(`{"name":"demo-1"}`))
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate POST: got %d, want 409", resp.StatusCode)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("first directory clobbered by failed second POST: %v", err)
	}
}

// TestCreateTopology_InvalidName covers the gate against shell/path
// abuse. The shared nameRE rejects empty names, names that begin with
// a hyphen, and names containing path separators.
func TestCreateTopology_InvalidName(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	cases := []struct {
		label string
		name  string
	}{
		{"empty", ""},
		{"leading-hyphen", "-demo"},
		{"slash", "demo/escape"},
		{"dotdot", ".."},
		{"with-space", "demo lab"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"name": tc.name})
			resp, _ := doRequest(t, ts, http.MethodPost, "/newtrun/v1/topologies", body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("name=%q: got %d, want 400", tc.name, resp.StatusCode)
			}
			// No spec directory should have been created. Checking the
			// specs/ subdir specifically avoids false positives from
			// degenerate path joins (e.g. "" → base, ".." → parent).
			if _, err := os.Stat(filepath.Join(srv.cfg.TopologiesBase, tc.name, "specs")); err == nil {
				t.Errorf("name=%q: specs/ subdir was created despite 400", tc.name)
			}
		})
	}
}

// TestCreateTopology_MalformedBody covers the JSON-decode gate. A
// non-JSON body must yield 400, not 500.
func TestCreateTopology_MalformedBody(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, _ := doRequest(t, ts, http.MethodPost, "/newtrun/v1/topologies",
		[]byte(`not json`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed body: got %d, want 400", resp.StatusCode)
	}
}

// TestCreateTopology_SeedsParseableSpecFiles is the round-trip test:
// the seeded spec files load cleanly through newtron's spec.Loader so
// the very next call — POST /newtron/v1/network with the returned
// spec_dir — works without manual edits. This is the issue's
// acceptance criterion ("the operator never touches the filesystem").
func TestCreateTopology_SeedsParseableSpecFiles(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp, body := doRequest(t, ts, http.MethodPost, "/newtrun/v1/topologies",
		[]byte(`{"name":"loader-ok"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	specDir := filepath.Join(srv.cfg.TopologiesBase, "loader-ok", "specs")
	loader := spec.NewLoader(specDir)
	if err := loader.Load(); err != nil {
		t.Fatalf("spec.Loader.Load on seeded directory: %v", err)
	}
}
