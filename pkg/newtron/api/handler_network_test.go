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

// register-network wire shape:
// POST /newtron/v1/networks accepts {id, scaffold?, description?}.
// The server resolves the dir from filepath.Join(NetworksBase, id);
// operators never see paths on the wire (§27, §33).

// TestRegisterNetwork_HappyPath_Scaffolds posts {id: foo} (scaffold
// absent) against a base whose <base>/<id> slot doesn't exist —
// server scaffolds + registers + returns NetworkInfo.
func TestRegisterNetwork_HappyPath_Scaffolds(t *testing.T) {
	base := t.TempDir()
	srv := NewServer(Config{NetworksBase: base})

	body, _ := json.Marshal(RegisterNetworkRequest{ID: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	dir := filepath.Join(base, "demo")
	for _, name := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s after scaffold: %v", name, err)
		}
	}
	if _, ok := srv.networks["demo"]; !ok {
		t.Errorf("network was not registered after scaffold")
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

// TestRegisterNetwork_HappyPath_RegistersExisting posts {id: foo} when
// the slot already carries a valid spec layout — server registers it
// idempotently rather than scaffolding (which would 409).
func TestRegisterNetwork_HappyPath_RegistersExisting(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed minimum-viable spec files so RegisterNetwork loads cleanly.
	for _, name := range []string{"network.json", "topology.json", "platforms.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	srv := NewServer(Config{NetworksBase: base})

	body, _ := json.Marshal(RegisterNetworkRequest{ID: "demo"})
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

// TestRegisterNetwork_ScaffoldTrue_409OnExisting confirms that with
// scaffold:true, an already-initialized slot returns 409 — the
// "force-create" intent refuses to silently reuse.
func TestRegisterNetwork_ScaffoldTrue_409OnExisting(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "topology.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := NewServer(Config{NetworksBase: base})

	body, _ := json.Marshal(RegisterNetworkRequest{ID: "demo", Scaffold: true})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if _, ok := srv.networks["demo"]; ok {
		t.Errorf("network should not be registered on scaffold conflict")
	}
}

// TestRegisterNetwork_IDValidation_Rejects pins the id regex contract:
// path separators, dots, and spaces are rejected with 400 before any
// filesystem work happens. The operator-facing error must name the
// constraint so the fix is obvious.
func TestRegisterNetwork_IDValidation_Rejects(t *testing.T) {
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
		if w.Code == http.StatusCreated {
			t.Errorf("id=%q should have been rejected, but got 201", id)
		}
	}
}

// TestRegisterNetwork_NoBase_Rejected pins the boot-time contract:
// when the server has no networks-base, it cannot resolve the dir
// and refuses the request rather than silently registering at "".
func TestRegisterNetwork_NoBase_Rejected(t *testing.T) {
	srv := NewServer(Config{})

	body, _ := json.Marshal(RegisterNetworkRequest{ID: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Errorf("registration without networks-base should fail; got 201")
	}
	if _, ok := srv.networks["demo"]; ok {
		t.Errorf("network should not be registered without a base")
	}
}
