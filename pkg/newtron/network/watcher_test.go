package network

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// quietLogger returns a logger that swallows output. Tests don't want
// the watcher's normal log lines polluting `go test -v`.
func quietLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// TestSpecWatcher_FileChangeTriggersReload pins the L6 revocation
// contract: editing a file under the watched network dir produces one
// reload(networkID) call within the debounce window. Without this
// behavior, operators would still need to POST /reload to revoke
// access — exactly the gap L6 closes.
func TestSpecWatcher_FileChangeTriggersReload(t *testing.T) {
	dir := t.TempDir()
	netFile := filepath.Join(dir, "network.json")
	if err := os.WriteFile(netFile, []byte(`{"version": "1.0"}`), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	var calls atomic.Int32
	got := make(chan string, 4)
	w, err := NewSpecWatcher(quietLogger(), 50*time.Millisecond, func(id string) error {
		calls.Add(1)
		got <- id
		return nil
	})
	if err != nil {
		t.Fatalf("NewSpecWatcher: %v", err)
	}
	defer w.Stop()
	if err := w.Add(dir, "default"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.Start(context.Background())

	// Mutate the watched file.
	if err := os.WriteFile(netFile, []byte(`{"version": "1.0", "super_users": ["root"]}`), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	select {
	case id := <-got:
		if id != "default" {
			t.Errorf("reload fired for id=%q, want default", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not fire within 2s of file write")
	}
}

// TestSpecWatcher_ZoneFileChangeTriggersReload pins that a write under the
// per-file zones/ subdirectory triggers a reload — the watch target added when
// zones moved to zones/<name>.json. inotify is non-recursive, so the parent
// watch alone would miss it; Add must register zones/ explicitly. (A subdir
// absent at Add is adopted when it appears — see
// TestSpecWatcher_AdoptsSubdirCreatedAfterAdd.)
func TestSpecWatcher_ZoneFileChangeTriggersReload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(`{"version":"1.0"}`), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	zonesDir := filepath.Join(dir, "zones")
	if err := os.MkdirAll(zonesDir, 0o755); err != nil {
		t.Fatalf("mkdir zones: %v", err)
	}

	got := make(chan string, 4)
	w, err := NewSpecWatcher(quietLogger(), 50*time.Millisecond, func(id string) error {
		got <- id
		return nil
	})
	if err != nil {
		t.Fatalf("NewSpecWatcher: %v", err)
	}
	defer w.Stop()
	if err := w.Add(dir, "default"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.Start(context.Background())

	// Author a zone override file under the watched zones/ subdir.
	if err := os.WriteFile(filepath.Join(zonesDir, "amer.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write zone: %v", err)
	}

	select {
	case id := <-got:
		if id != "default" {
			t.Errorf("reload fired for id=%q, want default", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not fire within 2s of a zones/ file write")
	}
}

// TestSpecWatcher_IgnoresAuditSubtree pins that writes into the network's
// own audit/ folder never trigger a reload. Audit is runtime output the
// server writes into the network folder (audit.Path); a reload on every
// logged mutation would be a storm. Creating the audit/ dir and appending
// to its log must stay silent — then a real network.json write proves the
// watcher is still alive (the zero-reload isn't a dead watcher).
func TestSpecWatcher_IgnoresAuditSubtree(t *testing.T) {
	dir := t.TempDir()
	netFile := filepath.Join(dir, "network.json")
	if err := os.WriteFile(netFile, []byte(`{"version":"1.0"}`), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	var calls atomic.Int32
	got := make(chan string, 4)
	w, err := NewSpecWatcher(quietLogger(), 50*time.Millisecond, func(id string) error {
		calls.Add(1)
		got <- id
		return nil
	})
	if err != nil {
		t.Fatalf("NewSpecWatcher: %v", err)
	}
	defer w.Stop()
	if err := w.Add(dir, "default"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.Start(context.Background())

	// Create audit/ (a Create event on the watched dir) and append to its
	// log (inside the unwatched subdir) — the writer's real activity.
	if err := os.MkdirAll(filepath.Join(dir, audit.AuditDirName), 0o755); err != nil {
		t.Fatalf("mkdir audit: %v", err)
	}
	logFile := audit.Path(dir)
	for i := range 5 {
		if err := os.WriteFile(logFile, []byte(`{"event":`+strconv.Itoa(i)+"}\n"), 0o644); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// Well past the debounce — any audit-triggered reload would have fired.
	time.Sleep(300 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("audit writes triggered %d reload(s); want 0", n)
	}

	// Liveness control: a real spec change still reloads.
	if err := os.WriteFile(netFile, []byte(`{"version":"1.0","super_users":["root"]}`), 0o644); err != nil {
		t.Fatalf("rewrite network.json: %v", err)
	}
	select {
	case <-got:
		// good — watcher is alive and reloaded on the real change.
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not reload on a real network.json change (dead watcher?)")
	}
}

// TestSpecWatcher_DebouncesRapidWrites pins the debounce behavior:
// a burst of writes within the debounce window coalesces into one
// reload call, not one per write. Editor saves frequently produce
// multiple events (write + rename + write) and the watcher must not
// invoke ReloadNetwork once per event.
func TestSpecWatcher_DebouncesRapidWrites(t *testing.T) {
	dir := t.TempDir()
	netFile := filepath.Join(dir, "network.json")
	if err := os.WriteFile(netFile, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	// The debounce is deliberately long relative to the burst. The property
	// under test is that writes arriving inside one window coalesce into a
	// single reload; with a 200ms window and 10ms between writes, a CI runner
	// stalling for 200ms mid-burst split the burst and produced two reloads —
	// a failure of the runner's scheduling, not of the coalescing. A window an
	// order of magnitude wider than the inter-write gap keeps the assertion
	// exact ("want 1") instead of weakening it to "fewer than 10".
	//
	// This is headroom, not proof: a stall longer than the window would still
	// split the burst. Making it airtight needs an injectable clock in place of
	// time.AfterFunc, which is a larger change than this test warrants.
	var calls atomic.Int32
	w, err := NewSpecWatcher(quietLogger(), 2*time.Second, func(id string) error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("NewSpecWatcher: %v", err)
	}
	defer w.Stop()
	if err := w.Add(dir, "default"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.Start(context.Background())

	// Burst: 10 writes within the debounce window.
	for i := range 10 {
		if err := os.WriteFile(netFile, []byte(`{"i":`+strconv.Itoa(i)+`}`), 0o644); err != nil {
			t.Fatalf("burst write %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait long enough for the debounce to settle plus one reload.
	time.Sleep(3 * time.Second)
	if n := calls.Load(); n != 1 {
		t.Errorf("got %d reload calls for a burst, want 1 (debounce coalesced)", n)
	}
}

// TestSpecWatcher_Remove pins that Remove stops further reloads for
// a path. After Remove, edits to the file produce no callback.
func TestSpecWatcher_Remove(t *testing.T) {
	dir := t.TempDir()
	netFile := filepath.Join(dir, "network.json")
	_ = os.WriteFile(netFile, []byte(`{}`), 0o644)

	var calls atomic.Int32
	w, err := NewSpecWatcher(quietLogger(), 50*time.Millisecond, func(id string) error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("NewSpecWatcher: %v", err)
	}
	defer w.Stop()
	if err := w.Add(dir, "default"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.Start(context.Background())

	if err := w.Remove(dir); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_ = os.WriteFile(netFile, []byte(`{"x":1}`), 0o644)
	time.Sleep(300 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Errorf("got %d reload calls after Remove, want 0", n)
	}
}


// TestSpecWatcher_AdoptsSubdirCreatedAfterAdd pins #469: a nodes/ or zones/
// subdirectory that does not exist at Add is picked up when it appears, so
// edits to per-file specs inside it still reload.
//
// Creating the subdir itself fires on the parent watch, so that step reloads
// either way. Everything AFTER it is the real assertion: inotify is
// non-recursive, so without the subdir watch nothing inside it is delivered —
// not the first file, not a later edit — and an operator's node-spec or zone
// change silently never took effect. (Verified: with the adoption call removed,
// this test fails at the first write inside nodes/.)
func TestSpecWatcher_AdoptsSubdirCreatedAfterAdd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(`{"version":"1.0"}`), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	got := make(chan string, 8)
	w, err := NewSpecWatcher(quietLogger(), 50*time.Millisecond, func(id string) error {
		got <- id
		return nil
	})
	if err != nil {
		t.Fatalf("NewSpecWatcher: %v", err)
	}
	defer w.Stop()
	// Registered with no nodes/ present — the case that used to leave it unwatched.
	if err := w.Add(dir, "default"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.Start(context.Background())

	drain := func(what string) {
		t.Helper()
		select {
		case <-got:
		case <-time.After(2 * time.Second):
			t.Fatalf("no reload within 2s after %s", what)
		}
	}

	nodesDir := filepath.Join(dir, "nodes")
	if err := os.Mkdir(nodesDir, 0o755); err != nil {
		t.Fatalf("mkdir nodes: %v", err)
	}
	drain("creating nodes/")

	nodeFile := filepath.Join(nodesDir, "leaf1.json")
	if err := os.WriteFile(nodeFile, []byte(`{"mgmt_ip":"127.0.0.1"}`), 0o644); err != nil {
		t.Fatalf("write node spec: %v", err)
	}
	drain("creating nodes/leaf1.json")

	// The real test: an edit wholly inside the late-created subdir.
	if err := os.WriteFile(nodeFile, []byte(`{"mgmt_ip":"127.0.0.2"}`), 0o644); err != nil {
		t.Fatalf("edit node spec: %v", err)
	}
	select {
	case id := <-got:
		if id != "default" {
			t.Errorf("reload fired for id=%q, want default", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("editing a file inside a nodes/ created after Add did not reload — the subdir is not being watched (#469)")
	}
}
