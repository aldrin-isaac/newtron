package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// emitNEvents appends N integrity-chained events to a fresh
// FileLoggerWithIntegrity at the returned path. Used by the
// integrity tests as the prelude — produces a hash-chained log file
// the rest of the test can verify or tamper with.
func emitNEvents(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := NewFileLoggerWithIntegrity(path, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLoggerWithIntegrity: %v", err)
	}
	defer l.Close()
	for i := range n {
		e := &Event{
			Timestamp: time.Now().UTC(),
			User:      "alice",
			Operation: "authcheck:spec.author",
			Service:   "svc-" + string(rune('a'+i)),
			Success:   true,
		}
		if err := l.Log(e); err != nil {
			t.Fatalf("Log event %d: %v", i, err)
		}
	}
	return path
}

// TestAuditIntegrity_HashChainClean pins the happy path: a freshly
// written integrity log verifies clean, every entry's ID reproduces,
// and Verify returns BrokenAt=0 with a populated Head.
func TestAuditIntegrity_HashChainClean(t *testing.T) {
	path := emitNEvents(t, 5)
	got, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.BrokenAt != 0 {
		t.Errorf("BrokenAt = %d, want 0 (%s)", got.BrokenAt, got.Reason)
	}
	if got.Entries != 5 {
		t.Errorf("Entries = %d, want 5", got.Entries)
	}
	if got.Head == "" {
		t.Error("Head is empty on a clean chain")
	}
}

// TestAuditIntegrity_TamperedEntryDetected pins the L6 contract:
// modifying any field of an emitted entry breaks the chain and
// Verify reports the breakpoint at the tampered line. This is the
// behavior an operator depends on after a suspected intrusion.
func TestAuditIntegrity_TamperedEntryDetected(t *testing.T) {
	path := emitNEvents(t, 4)

	// Read all lines, mutate the third entry's User field in place,
	// rewrite. The mutation changes canonical JSON, which changes
	// the required ID, which is now wrong on disk.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	var e Event
	if err := json.Unmarshal([]byte(lines[2]), &e); err != nil {
		t.Fatalf("unmarshal line 3: %v", err)
	}
	e.User = "mallory" // mutate WITHOUT recomputing the hash
	tampered, err := json.Marshal(&e)
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}
	lines[2] = string(tampered)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	got, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.BrokenAt != 3 {
		t.Errorf("BrokenAt = %d, want 3 (tampered line); reason = %s", got.BrokenAt, got.Reason)
	}
	if !strings.Contains(got.Reason, "hash mismatch") {
		t.Errorf("Reason = %q, want it to mention hash mismatch", got.Reason)
	}
}

// TestAuditIntegrity_ChainContinuesAcrossRestart pins the restart
// behavior: closing and reopening a FileLoggerWithIntegrity recovers
// the chain head from disk, so events emitted after restart link to
// the events emitted before. The whole file then verifies as one
// chain end to end.
func TestAuditIntegrity_ChainContinuesAcrossRestart(t *testing.T) {
	path := emitNEvents(t, 2)

	// Reopen and emit two more.
	l, err := NewFileLoggerWithIntegrity(path, RotationConfig{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l.Close()
	for i := range 2 {
		e := &Event{
			Timestamp: time.Now().UTC(),
			User:      "bob",
			Operation: "authcheck:device.write",
			Service:   "post-restart-" + string(rune('a'+i)),
			Success:   true,
		}
		if err := l.Log(e); err != nil {
			t.Fatalf("Log post-restart %d: %v", i, err)
		}
	}

	got, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.BrokenAt != 0 {
		t.Errorf("BrokenAt = %d, want 0 — chain should continue across restart (%s)", got.BrokenAt, got.Reason)
	}
	if got.Entries != 4 {
		t.Errorf("Entries = %d, want 4 (2 pre-restart + 2 post-restart)", got.Entries)
	}
}

// TestAuditIntegrity_EmptyLogVerifiesClean pins the corner case: a
// missing or empty audit log verifies clean (no entries, no head).
// Without this, an operator running `audit verify` for the first
// time would see an error and assume tampering.
func TestAuditIntegrity_EmptyLogVerifiesClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.jsonl")
	got, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.BrokenAt != 0 || got.Entries != 0 {
		t.Errorf("missing file: got %+v, want clean+zero entries", got)
	}
}

// TestAuditIntegrity_DisabledLeavesEmptyIDs pins that the
// non-integrity FileLogger preserves pre-L6 behavior: every entry's
// ID stays empty, no PrevHash appears. Operators who don't opt in
// see no hash overhead.
func TestAuditIntegrity_DisabledLeavesEmptyIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noint.jsonl")
	l, err := NewFileLogger(path, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer l.Close()
	if err := l.Log(&Event{User: "alice", Operation: "x", Success: true}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var e Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.ID != "" {
		t.Errorf("ID = %q, want empty when integrity is off", e.ID)
	}
	if e.PrevHash != "" {
		t.Errorf("PrevHash = %q, want empty when integrity is off", e.PrevHash)
	}
}
