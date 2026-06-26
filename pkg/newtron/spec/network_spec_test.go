package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNetworkSpecFile_DescriptionRoundTrips pins that a top-level network
// description survives load → save → load. Before Description was modeled on
// NetworkSpecFile, json.Unmarshal silently dropped it as an unknown field and
// SaveNetwork (json.MarshalIndent) re-serialized without it — the round-trip
// loss that motivated modeling the field.
func TestNetworkSpecFile_DescriptionRoundTrips(t *testing.T) {
	const want = "Grant table the auth suite exercises end to end."

	// Load: a top-level "description" lands on the struct field.
	var loaded NetworkSpecFile
	if err := json.Unmarshal([]byte(`{"version":"1.0","description":"`+want+`"}`), &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if loaded.Description != want {
		t.Fatalf("Description not loaded: got %q, want %q", loaded.Description, want)
	}

	// Save: SaveNetwork's json.MarshalIndent re-emits it.
	out, err := json.Marshal(&loaded)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), `"description":"`+want+`"`) {
		t.Errorf("Description not re-serialized; got %s", out)
	}

	// omitempty: a network without a description stays clean on round-trip —
	// no spurious empty "description" field written.
	empty, err := json.Marshal(&NetworkSpecFile{Version: "1.0"})
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	if strings.Contains(string(empty), "description") {
		t.Errorf("empty Description should be omitted; got %s", empty)
	}
}
