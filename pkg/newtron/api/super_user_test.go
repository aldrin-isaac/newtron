package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// TestSuperUserManagement_HTTP — an authorized operator adds/removes per-network
// super-users through the API (no file editing), and the change is reflected in
// GET /authorization and persisted to network.json. Idempotent both ways.
func TestSuperUserManagement_HTTP(t *testing.T) {
	// Temp copy — the mutation persists network.json; keep the fixture clean.
	src := filepath.Join(repoRoot(t), "networks", "1node-vs")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	s := NewServer(Config{}) // authz off → meta-authz gate is a no-op; tests the mechanics
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(httptest.NewRequest("", "/", nil).Context()) })

	do := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		w := httptest.NewRecorder()
		s.HTTPServer().Handler.ServeHTTP(w, r)
		return w
	}
	superUsers := func() []string {
		w := do("GET", "/newtron/v1/networks/default/authorization", "")
		var env struct {
			Data newtron.AuthorizationDetail `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Data.SuperUsers
	}
	contains := func(xs []string, v string) bool {
		for _, x := range xs {
			if x == v {
				return true
			}
		}
		return false
	}

	// Add carol.
	if w := do("POST", "/newtron/v1/networks/default/super-users", `{"user":"carol"}`); w.Code != http.StatusOK {
		t.Fatalf("add: status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !contains(superUsers(), "carol") {
		t.Fatalf("after add, super_users=%v, want carol present", superUsers())
	}
	// Persisted to network.json.
	raw, _ := os.ReadFile(filepath.Join(dir, "network.json"))
	if !strings.Contains(string(raw), "carol") {
		t.Error("add did not persist carol to network.json")
	}
	// Idempotent add.
	if w := do("POST", "/newtron/v1/networks/default/super-users", `{"user":"carol"}`); w.Code != http.StatusOK {
		t.Errorf("idempotent add: status=%d, want 200", w.Code)
	}

	// Missing user → 400.
	if w := do("POST", "/newtron/v1/networks/default/super-users", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty user: status=%d, want 400", w.Code)
	}

	// Remove carol.
	if w := do("DELETE", "/newtron/v1/networks/default/super-users/carol", ""); w.Code != http.StatusOK {
		t.Fatalf("remove: status=%d, want 200", w.Code)
	}
	if contains(superUsers(), "carol") {
		t.Errorf("after remove, super_users=%v, want carol absent", superUsers())
	}
	// Idempotent remove.
	if w := do("DELETE", "/newtron/v1/networks/default/super-users/carol", ""); w.Code != http.StatusOK {
		t.Errorf("idempotent remove: status=%d, want 200", w.Code)
	}
}
