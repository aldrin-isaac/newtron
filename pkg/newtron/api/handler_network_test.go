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
	specDir := filepath.Join(t.TempDir(), "specs")
	srv := NewServer(nil, 0, nil)

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
	if _, err := os.Stat(filepath.Join(specDir, "profiles")); err != nil {
		t.Errorf("expected profiles/ after scaffold: %v", err)
	}
	if _, ok := srv.networks["demo-1"]; !ok {
		t.Errorf("network was not registered after scaffold")
	}
}

// TestRegisterNetwork_Scaffold_ConflictReturns409 ensures a pre-existing
// spec_dir maps the spec-package sentinel error onto 409.
func TestRegisterNetwork_Scaffold_ConflictReturns409(t *testing.T) {
	specDir := filepath.Join(t.TempDir(), "specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "topology.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := NewServer(nil, 0, nil)
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
	specDir := filepath.Join(t.TempDir(), "specs")

	srv := NewServer(nil, 0, nil)
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

// TestRegisterNetwork_AlreadyRegistered_409EnvelopeCarriesExistingSpecDir
// pins issue #117: when the network id is already registered, the 409
// response includes the existing spec_dir under envelope.Data as
// AlreadyRegisteredErrorInfo, so the client can distinguish true-idempotent
// re-registration (same spec_dir → silent success) from a real conflict
// (different spec_dir → typed error). Without this, the client's old
// silent-409 swallow masks the conflict.
func TestRegisterNetwork_AlreadyRegistered_409EnvelopeCarriesExistingSpecDir(t *testing.T) {
	specDir := filepath.Join(t.TempDir(), "specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	srv := NewServer(nil, 0, nil)
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
