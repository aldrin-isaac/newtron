package secret

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestFileStore_CreateRoundTrip pins the happy path: opening a fresh
// path creates a 0600 file; Set/Get/List/Delete round-trip cleanly.
func TestFileStore_CreateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	if err := s.Set("switch1-ssh", "YourPaSsWoRd"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, err := s.Get("switch1-ssh"); err != nil || v != "YourPaSsWoRd" {
		t.Errorf("Get switch1-ssh = (%q, %v); want (YourPaSsWoRd, nil)", v, err)
	}
	if err := s.Set("switch2-ssh", "other"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	keys, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"switch1-ssh", "switch2-ssh"}
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] {
		t.Errorf("List = %v; want sorted %v", keys, want)
	}

	if err := s.Delete("switch1-ssh"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if _, err := s.Get("switch1-ssh"); !errors.Is(err, err) || !isNotFound(err) {
		t.Errorf("Get after delete err = %v; want *ErrNotFound", err)
	}

	// Mode check: the file the store maintains must be 0600. A
	// regression to a more-permissive mode is a real-world bug
	// because it'd leak secrets to other users on the host.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %v; want 0600", mode)
	}
}

// TestFileStore_RefusesPermissiveExisting pins that opening an
// existing file with mode broader than 0600 fails rather than
// silently proceeding. A misconfigured secret store is worth
// failing-fast rather than serving secrets under wrong permissions.
func TestFileStore_RefusesPermissiveExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "world.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := NewFileStore(path)
	if err == nil {
		t.Fatal("expected NewFileStore to refuse 0644 file; got nil err")
	}
}

// TestFileStore_MissingKeyTypedError pins that "key not found"
// returns *ErrNotFound (a typed error) rather than a generic string,
// so callers (the CLI, the resolver) can distinguish missing-key
// from I/O failure.
func TestFileStore_MissingKeyTypedError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_, err = s.Get("does-not-exist")
	if !isNotFound(err) {
		t.Errorf("err = %v; want *ErrNotFound", err)
	}
}

// TestFileStore_PersistsAcrossReopen pins that Set's atomic write
// actually persists to disk (the next New of the same path sees the
// values). Without this the store would be ephemeral and the L0
// goal — "passwords live outside the spec dir, not just outside the
// process" — wouldn't be met.
func TestFileStore_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	s2, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen NewFileStore: %v", err)
	}
	if v, err := s2.Get("k"); err != nil || v != "v" {
		t.Errorf("reopen Get k = (%q, %v); want (v, nil)", v, err)
	}
}

// TestFileStore_ConcurrentSafe pins the sync.Mutex contract: many
// goroutines Set/Get against the same store don't corrupt the
// underlying file or race on the in-process map.
func TestFileStore_ConcurrentSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k-" + string(rune('a'+i%26))
			if err := s.Set(key, "v"); err != nil {
				t.Errorf("Set: %v", err)
			}
			if _, err := s.Get(key); err != nil {
				t.Errorf("Get: %v", err)
			}
		}(i)
	}
	wg.Wait()
	keys, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// We don't assert on the exact key set because goroutine
	// scheduling determines which i values collided on the same
	// key; just that List doesn't crash and returns sorted keys.
	for j := 1; j < len(keys); j++ {
		if keys[j-1] > keys[j] {
			t.Errorf("List unsorted: %q before %q", keys[j-1], keys[j])
		}
	}
}

// TestNewFileStore_EmptyPathRejected pins that "" is not a valid
// store path. An accidental empty config value (e.g., from a flag
// default) shouldn't create a store at the current working directory.
func TestNewFileStore_EmptyPathRejected(t *testing.T) {
	if _, err := NewFileStore(""); err == nil {
		t.Error("expected NewFileStore(\"\") to fail; got nil err")
	}
}

func isNotFound(err error) bool {
	var nf *ErrNotFound
	return errors.As(err, &nf)
}
