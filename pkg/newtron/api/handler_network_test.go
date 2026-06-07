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
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/network", bytes.NewReader(body))
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
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/network", bytes.NewReader(body))
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
	req := httptest.NewRequest(http.MethodPost, "/newtron/v1/network", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		t.Fatalf("expected non-201 for missing spec_dir without scaffold; got 201")
	}
	if _, ok := srv.networks["demo-3"]; ok {
		t.Errorf("network should not be registered when spec_dir is empty")
	}
}
