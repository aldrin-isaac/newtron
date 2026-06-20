package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// POST /newtron/v1/networks wire shape:
//   {id, description?} → idempotent create.
// Server resolves dir from filepath.Join(NetworksBase, id);
// operators never see paths on the wire (§27, §33).
//
// Status code carries the new-vs-existed distinction:
//   201 Created on first registration; 200 OK on subsequent calls.

// TestCreateNetwork_HappyPath_CreatesEmpty posts {id: demo} against
// a base whose <base>/<id> slot doesn't exist — server creates the
// empty spec layout, registers it, and returns 201.
func TestCreateNetwork_HappyPath_CreatesEmpty(t *testing.T) {
	base := t.TempDir()
	srv := NewServer(Config{NetworksBase: base})

	body, _ := json.Marshal(CreateNetworkRequest{ID: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	dir := filepath.Join(base, "demo")
	for _, name := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s after create: %v", name, err)
		}
	}
	if _, ok := srv.networks["demo"]; !ok {
		t.Errorf("network was not registered after create")
	}

	var env struct {
		Data NetworkInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
	}
	if env.Data.ID != "demo" || env.Data.Dir != dir {
		t.Errorf("response NetworkInfo = %+v, want id=demo dir=%s", env.Data, dir)
	}
}

// TestCreateNetwork_HappyPath_RegistersExisting posts {id: demo} when
// the slot already carries a valid spec layout — server registers it
// rather than re-creating, still 201 since it's not yet in memory.
func TestCreateNetwork_HappyPath_RegistersExisting(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"network.json", "topology.json", "platforms.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	srv := NewServer(Config{NetworksBase: base})

	body, _ := json.Marshal(CreateNetworkRequest{ID: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if _, ok := srv.networks["demo"]; !ok {
		t.Errorf("network was not registered")
	}
}

// TestCreateNetwork_Idempotent_Returns200 confirms that a second
// create for the same id returns 200 OK (not 201, not 409) with the
// existing NetworkInfo. The status code is the only signal operators
// need to distinguish "I just made this" from "it was already here."
func TestCreateNetwork_Idempotent_Returns200(t *testing.T) {
	base := t.TempDir()
	srv := NewServer(Config{NetworksBase: base})

	body, _ := json.Marshal(CreateNetworkRequest{ID: "demo"})

	// First call creates (201).
	req1 := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first call: status = %d, want 201", w1.Code)
	}

	// Second call is idempotent (200).
	req2 := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second call: status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}

	// Both responses describe the same network.
	var env1, env2 struct {
		Data NetworkInfo `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &env1)
	_ = json.Unmarshal(w2.Body.Bytes(), &env2)
	if env1.Data.ID != env2.Data.ID || env1.Data.Dir != env2.Data.Dir {
		t.Errorf("idempotent calls disagree: %+v vs %+v", env1.Data, env2.Data)
	}
}

// TestCreateNetwork_IDValidation_Rejects pins the id regex contract:
// path separators, dots, and spaces are rejected with 400 before any
// filesystem work happens. The operator-facing error must name the
// constraint so the fix is obvious.
func TestCreateNetwork_IDValidation_Rejects(t *testing.T) {
	base := t.TempDir()
	srv := NewServer(Config{NetworksBase: base})

	for _, id := range []string{
		"with/slash",
		"with.dot",
		"with space",
		"",
		"way-too-long-" + string(make([]byte, 100)),
	} {
		body, _ := json.Marshal(map[string]any{"id": id})
		req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusCreated || w.Code == http.StatusOK {
			t.Errorf("id=%q should have been rejected, but got %d", id, w.Code)
		}
	}
}

// TestCreateNetwork_NoBase_Rejected pins the boot-time contract:
// when the server has no networks-base, it cannot resolve the dir
// and refuses the request rather than silently registering at "".
func TestCreateNetwork_NoBase_Rejected(t *testing.T) {
	srv := NewServer(Config{})

	body, _ := json.Marshal(CreateNetworkRequest{ID: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusCreated || w.Code == http.StatusOK {
		t.Errorf("registration without networks-base should fail; got %d", w.Code)
	}
	if _, ok := srv.networks["demo"]; ok {
		t.Errorf("network should not be registered without a base")
	}
}
