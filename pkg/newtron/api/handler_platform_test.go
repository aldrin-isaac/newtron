package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// scaffoldPlatformNetwork registers a fresh network with id "default"
// at a scaffolded network dir. Returns the server. Used by the #173
// platform CRUD tests.
func scaffoldPlatformNetwork(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	if err := spec.CreateEmpty(dir, "platform CRUD test fixture"); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	s := NewServer(Config{})
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s, dir
}

// postPlatform sends a JSON POST and returns the recorder.
func postPlatform(t *testing.T, s *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

// TestPlatform_CreateUpdateDelete_RoundTrip exercises the full
// lifecycle through the production HTTP path: scaffold a network,
// create a platform via POST, update a specific field, read it back,
// delete it, and confirm subsequent show is 404. The assertion
// targets the changed field (description) so a regression that
// silently drops Update's effect would surface as a specific
// assertion failure — §16 honest tests requires the change be
// observable, not implied.
func TestPlatform_CreateUpdateDelete_RoundTrip(t *testing.T) {
	s, specDir := scaffoldPlatformNetwork(t)

	if w := postPlatform(t, s, "/newtron/v1/networks/default/create-platform", map[string]any{
		"name":          "my-vs",
		"hwsku":         "Force10-S6000",
		"description":   "initial",
		"port_count":    32,
		"default_speed": "100G",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}

	// On-disk verification: platforms.json now contains the new entry
	// (catches a regression where the in-memory mutation succeeded
	// but the persist path didn't fire).
	if data, err := os.ReadFile(filepath.Join(specDir, "platforms.json")); err != nil {
		t.Fatalf("read platforms.json after create: %v", err)
	} else if !strings.Contains(string(data), `"my-vs"`) {
		t.Errorf("platforms.json missing 'my-vs' entry after create:\n%s", data)
	}

	if w := postPlatform(t, s, "/newtron/v1/networks/default/update-platform", map[string]any{
		"name":          "my-vs",
		"hwsku":         "Force10-S6000",
		"description":   "updated",
		"port_count":    32,
		"default_speed": "100G",
	}); w.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/platforms/my-vs", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("show after update: status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode show: %v; body: %s", err, w.Body.String())
	}
	if env.Data.Description != "updated" {
		t.Errorf("description: got %q, want %q — UpdatePlatform did not take effect", env.Data.Description, "updated")
	}

	if w := postPlatform(t, s, "/newtron/v1/networks/default/delete-platform", map[string]any{
		"name": "my-vs",
	}); w.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", w.Code, w.Body.String())
	}

	// Show after delete: 404.
	req = httptest.NewRequest(http.MethodGet, "/newtron/v1/networks/default/platforms/my-vs", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("show after delete: status=%d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestPlatform_Update_NotFound pins the 404 contract for Update:
// without the existence check in Network.UpdatePlatform the call
// would behave like Create (silently store the entry), eroding the
// Update/Create distinction and breaking operator intuition.
func TestPlatform_Update_NotFound(t *testing.T) {
	s, _ := scaffoldPlatformNetwork(t)

	w := postPlatform(t, s, "/newtron/v1/networks/default/update-platform", map[string]any{
		"name":          "missing-platform",
		"hwsku":         "Force10-S6000",
		"port_count":    32,
		"default_speed": "100G",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestPlatform_Delete_RefusesReferringProfile pins the referential
// integrity contract: a profile holding Platform == "<name>" blocks
// DeletePlatform with 409 ConflictError; the operator must retarget
// or delete the referring profiles first. The test creates a
// platform, creates a profile referencing it, attempts delete-platform,
// and asserts 409. Without the check the platform would vanish and
// the profile's Platform field would point at a stale identifier —
// breaking subsequent reads.
func TestPlatform_Delete_RefusesReferringProfile(t *testing.T) {
	s, _ := scaffoldPlatformNetwork(t)

	if w := postPlatform(t, s, "/newtron/v1/networks/default/create-platform", map[string]any{
		"name":          "edge-vs",
		"hwsku":         "Force10-S6000",
		"port_count":    32,
		"default_speed": "100G",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-platform: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := postPlatform(t, s, "/newtron/v1/networks/default/create-zone", map[string]any{
		"name": "amer",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-zone: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := postPlatform(t, s, "/newtron/v1/networks/default/create-profile", map[string]any{
		"name":     "switch1",
		"mgmt_ip":  "10.0.0.1",
		"zone":     "amer",
		"platform": "edge-vs",
	}); w.Code != http.StatusCreated {
		t.Fatalf("create-profile: status=%d body=%s", w.Code, w.Body.String())
	}

	w := postPlatform(t, s, "/newtron/v1/networks/default/delete-platform", map[string]any{
		"name": "edge-vs",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("delete-platform with referring profile: status=%d, want 409; body=%s", w.Code, w.Body.String())
	}
}

// TestPlatform_Create_Duplicate pins the 409-on-duplicate contract:
// creating a platform with a name that already exists must surface a
// conflict, not silently overwrite.
func TestPlatform_Create_Duplicate(t *testing.T) {
	s, _ := scaffoldPlatformNetwork(t)

	body := map[string]any{
		"name":          "dup",
		"hwsku":         "Force10-S6000",
		"port_count":    32,
		"default_speed": "100G",
	}
	if w := postPlatform(t, s, "/newtron/v1/networks/default/create-platform", body); w.Code != http.StatusCreated {
		t.Fatalf("first create: status=%d body=%s", w.Code, w.Body.String())
	}
	w := postPlatform(t, s, "/newtron/v1/networks/default/create-platform", body)
	if w.Code < 400 {
		t.Fatalf("duplicate create: status=%d, want 4xx; body=%s", w.Code, w.Body.String())
	}
}
