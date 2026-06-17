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

// TestRegisterNetwork_Scaffold_HappyPath posts {scaffold:true} to a fresh
// spec_dir and asserts the directory was scaffolded with the three seed
// specs and the network was registered.
func TestRegisterNetwork_Scaffold_HappyPath(t *testing.T) {
	specDir := t.TempDir()
	srv := NewServer(Config{})

	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:          "demo-1",
		SpecDir:     specDir,
		Scaffold:    true,
		Description: "scaffold test",
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	for _, name := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(specDir, name)); err != nil {
			t.Errorf("expected %s after scaffold: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(specDir, "nodes")); err != nil {
		t.Errorf("expected profiles/ after scaffold: %v", err)
	}
	if _, ok := srv.networks["demo-1"]; !ok {
		t.Errorf("network was not registered after scaffold")
	}
}

// TestRegisterNetwork_Scaffold_ConflictReturns409 ensures a pre-existing
// spec_dir maps the spec-package sentinel error onto 409.
func TestRegisterNetwork_Scaffold_ConflictReturns409(t *testing.T) {
	specDir := t.TempDir()
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "topology.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := NewServer(Config{})
	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:       "demo-2",
		SpecDir:  specDir,
		Scaffold: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if _, ok := srv.networks["demo-2"]; ok {
		t.Errorf("network should not be registered on scaffold conflict")
	}
}

// TestRegisterNetwork_NoScaffold_MissingSpecDir confirms the default
// (register-existing) flow still rejects an empty spec_dir — the scaffold
// extension must not alter the contract for the existing call site.
func TestRegisterNetwork_NoScaffold_MissingSpecDir(t *testing.T) {
	specDir := t.TempDir()

	srv := NewServer(Config{})
	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:      "demo-3",
		SpecDir: specDir,
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Fatalf("expected non-201 for missing spec_dir without scaffold; got 201")
	}
	if _, ok := srv.networks["demo-3"]; ok {
		t.Errorf("network should not be registered when spec_dir is empty")
	}
}

// TestRegisterNetwork_Scaffold_DerivedSpecDir_HappyPath posts
// {id, scaffold:true} with no spec_dir against a server configured with
// a scaffold root, and asserts the directory was scaffolded at
// <root>/<id>, the network was registered, and the response carries
// the resolved spec_dir in the canonical NetworkInfo shape (#122).
func TestRegisterNetwork_Scaffold_DerivedSpecDir_HappyPath(t *testing.T) {
	root := t.TempDir()
	srv := NewServer(Config{ScaffoldRoot: root})

	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:          "demo-derived",
		Scaffold:    true,
		Description: "derived path test",
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	derived := filepath.Join(root, "demo-derived")
	for _, name := range []string{"topology.json", "platforms.json", "network.json"} {
		if _, err := os.Stat(filepath.Join(derived, name)); err != nil {
			t.Errorf("expected %s at derived path %s: %v", name, derived, err)
		}
	}

	// Response is the canonical NetworkInfo wrapped in the standard
	// envelope; verify the resolved spec_dir matches what the server
	// derived.
	var env struct {
		Data NetworkInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
	}
	if env.Data.ID != "demo-derived" {
		t.Errorf("Data.ID = %q, want demo-derived", env.Data.ID)
	}
	if env.Data.SpecDir != derived {
		t.Errorf("Data.SpecDir = %q, want %q", env.Data.SpecDir, derived)
	}
}

// TestRegisterNetwork_Scaffold_DerivedSpecDir_NoRoot rejects derived-path
// mode when the server has no scaffold root configured — the operator
// must opt in by setting --scaffold-root rather than the server picking
// a default that might be wrong for the deployment (#122).
func TestRegisterNetwork_Scaffold_DerivedSpecDir_NoRoot(t *testing.T) {
	srv := NewServer(Config{}) // no scaffold root

	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:       "demo-no-root",
		Scaffold: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Fatalf("expected non-201 for derived mode without scaffold root; got 201; body=%s", w.Body.String())
	}
	if _, ok := srv.networks["demo-no-root"]; ok {
		t.Errorf("network should not be registered when scaffold root is unset")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("scaffold-root")) {
		t.Errorf("error should mention --scaffold-root so operator knows the fix; body=%s", w.Body.String())
	}
}

// TestRegisterNetwork_Scaffold_DerivedSpecDir_Conflict verifies the
// existing-layout case still maps to 409: if <root>/<id> already
// carries spec files, the server refuses to overwrite — same contract
// as the explicit-path collision case, so the wire behavior doesn't
// depend on who picked the path (#122).
func TestRegisterNetwork_Scaffold_DerivedSpecDir_Conflict(t *testing.T) {
	root := t.TempDir()
	derived := filepath.Join(root, "demo-collide")
	if err := os.MkdirAll(derived, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(derived, "topology.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := NewServer(Config{ScaffoldRoot: root})
	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:       "demo-collide",
		Scaffold: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if _, ok := srv.networks["demo-collide"]; ok {
		t.Errorf("network should not be registered on derived-path scaffold conflict")
	}
}

// TestRegisterNetwork_201ResponseCarriesNetworkInfo verifies the success
// response on the existing explicit-path scaffold path also returns
// NetworkInfo — the response shape is uniform across the two modes so
// clients don't have to branch on input style.
func TestRegisterNetwork_201ResponseCarriesNetworkInfo(t *testing.T) {
	specDir := t.TempDir()
	srv := NewServer(Config{})

	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:       "demo-info",
		SpecDir:  specDir,
		Scaffold: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data NetworkInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
	}
	if env.Data.ID != "demo-info" {
		t.Errorf("Data.ID = %q, want demo-info", env.Data.ID)
	}
	if env.Data.SpecDir != specDir {
		t.Errorf("Data.SpecDir = %q, want %q", env.Data.SpecDir, specDir)
	}
}

// TestRegisterNetwork_AlreadyRegistered_409EnvelopeCarriesExistingSpecDir
// pins issue #117: when the network id is already registered, the 409
// response includes the existing spec_dir under envelope.Data as
// AlreadyRegisteredErrorInfo, so the client can distinguish true-idempotent
// re-registration (same spec_dir → silent success) from a real conflict
// (different spec_dir → typed error). Without this, the client's old
// silent-409 swallow masks the conflict.
func TestRegisterNetwork_AlreadyRegistered_409EnvelopeCarriesExistingSpecDir(t *testing.T) {
	specDir := t.TempDir()
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	srv := NewServer(Config{})
	body, _ := json.Marshal(RegisterNetworkRequest{
		ID:       "demo-4",
		SpecDir:  specDir,
		Scaffold: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup register failed: status = %d, body=%s", w.Code, w.Body.String())
	}

	// Now re-register the same id pointing at a different spec_dir.
	otherDir := filepath.Join(t.TempDir(), "other-specs")
	body, _ = json.Marshal(RegisterNetworkRequest{
		ID:      "demo-4",
		SpecDir: otherDir,
	})
	req = httptest.NewRequest(http.MethodPost, "/newtron/v1/networks", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}

	// Parse the envelope and verify Data carries AlreadyRegisteredErrorInfo
	// with the original spec_dir, not the requested one.
	var env struct {
		Error string                     `json:"error"`
		Data  AlreadyRegisteredErrorInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
	}
	if env.Data.ID != "demo-4" {
		t.Errorf("Data.ID = %q, want demo-4", env.Data.ID)
	}
	if env.Data.ExistingSpecDir != specDir {
		t.Errorf("Data.ExistingSpecDir = %q, want %q", env.Data.ExistingSpecDir, specDir)
	}
	if env.Error == "" {
		t.Errorf("envelope.Error should not be empty on 409")
	}
}
