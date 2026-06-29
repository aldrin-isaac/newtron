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

// TestScopeAwareSpecRead — the read side mirrors the scope-aware write side: a
// spec detail can be read at network base (default) or at a specific zone/node
// override, returning that scope's own stored definition with no base fallback.
// Uses prefix-lists (the simplest scoped kind: name → CIDRs, no cross-refs).
func TestScopeAwareSpecRead(t *testing.T) {
	// Temp copy — the test authors specs that persist network.json.
	src := filepath.Join(repoRoot(t), "networks", "2node-vs") // has zone "amer"
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	s := NewServer(Config{})
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
	prefixesOf := func(w *httptest.ResponseRecorder) []string {
		var env struct {
			Data newtron.PrefixListDetail `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Data.Prefixes
	}
	eq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	const base = "/newtron/v1/networks/default"

	// Author a network-base prefix-list, then an amer-zone override (network
	// floor satisfied by the base) with different prefixes.
	if w := do("POST", base+"/create-prefix-list", `{"name":"TESTPL","prefixes":["10.0.0.0/8"]}`); w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create base: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := do("POST", base+"/create-prefix-list", `{"scope":"zone","scope_instance":"amer","name":"TESTPL","prefixes":["192.168.0.0/16"]}`); w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone override: status=%d body=%s", w.Code, w.Body.String())
	}

	// 1. No scope → network base.
	if w := do("GET", base+"/prefix-lists/TESTPL", ""); w.Code != http.StatusOK || !eq(prefixesOf(w), []string{"10.0.0.0/8"}) {
		t.Errorf("base read: status=%d prefixes=%v, want [10.0.0.0/8]", w.Code, prefixesOf(w))
	}
	// 2. scope=zone → the override's own stored prefixes (NOT the base).
	if w := do("GET", base+"/prefix-lists/TESTPL?scope=zone&scope_instance=amer", ""); w.Code != http.StatusOK || !eq(prefixesOf(w), []string{"192.168.0.0/16"}) {
		t.Errorf("zone read: status=%d prefixes=%v, want [192.168.0.0/16]", w.Code, prefixesOf(w))
	}

	// 3. A base-only list, read at zone scope → 404 (no fallback to base).
	if w := do("POST", base+"/create-prefix-list", `{"name":"BASEONLY","prefixes":["172.16.0.0/12"]}`); w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create BASEONLY: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := do("GET", base+"/prefix-lists/BASEONLY?scope=zone&scope_instance=amer", ""); w.Code != http.StatusNotFound {
		t.Errorf("zone read of base-only list: status=%d, want 404 (no base fallback); body=%s", w.Code, w.Body.String())
	}

	// 4. scope without instance → 400 (validation).
	if w := do("GET", base+"/prefix-lists/TESTPL?scope=zone", ""); w.Code != http.StatusBadRequest {
		t.Errorf("scope=zone, no instance: status=%d, want 400", w.Code)
	}
	// 5. unknown zone → 404.
	if w := do("GET", base+"/prefix-lists/TESTPL?scope=zone&scope_instance=nosuchzone", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown zone: status=%d, want 404", w.Code)
	}
	// 6. unknown name at base → 404 (also covers the prior 500-on-missing bug).
	if w := do("GET", base+"/prefix-lists/NOSUCH", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown name: status=%d, want 404", w.Code)
	}
}
