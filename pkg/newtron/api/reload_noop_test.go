package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestReloadNetwork_UnchangedSpecsKeepsLiveEntity pins the property that makes
// --spec-watch safe to leave on: a reload for specs that have not changed is a
// no-op that leaves the running networkEntity — and therefore its node actors,
// in-flight requests, and SSH sessions — untouched. Without it, every spec write
// newtron performs would drain the network's live device connections about a
// second later, because the watcher sees the server's own write.
func TestReloadNetwork_UnchangedSpecsKeepsLiveEntity(t *testing.T) {
	specDir := copyTestSpecDir(t)
	s := newTestServer(t)
	if err := s.RegisterNetwork("t", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	before := s.getNetwork("t")
	if before == nil {
		t.Fatal("network not registered")
	}

	if err := s.ReloadNetwork("t"); err != nil {
		t.Fatalf("ReloadNetwork (unchanged): %v", err)
	}
	if after := s.getNetwork("t"); after != before {
		t.Error("reload with unchanged specs replaced the live entity — actors and SSH sessions would have been drained")
	}

	// A real edit must still swap the entity in, or an operator's change
	// (a revoked grant) would never take effect.
	netJSON := filepath.Join(specDir, "network.json")
	data, err := os.ReadFile(netJSON)
	if err != nil {
		t.Fatalf("read network.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse network.json: %v", err)
	}
	doc["description"] = "edited by an operator"
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal network.json: %v", err)
	}
	if err := os.WriteFile(netJSON, edited, 0o644); err != nil {
		t.Fatalf("edit network.json: %v", err)
	}
	if err := s.ReloadNetwork("t"); err != nil {
		t.Fatalf("ReloadNetwork (edited): %v", err)
	}
	if after := s.getNetwork("t"); after == before {
		t.Error("reload after an external edit did not swap the entity — the change would never take effect")
	}
}
