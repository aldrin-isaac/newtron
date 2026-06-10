// Package secret provides the operator-configured backing store for
// secret material referenced from newtron spec files (auth-design.md
// L0). Spec values may contain references of the form
// "${secret:KEY}"; the Resolve helper in this package looks the key
// up in a Store and substitutes the stored value at load time.
//
// The package exposes a Store interface so a deployment can plug in
// any backend (an age-encrypted file, an HSM-backed KMS, the
// operator's existing secret-management tool). One in-tree backend
// is provided: FileStore, a JSON map stored at an operator-supplied
// path with file-system permissions enforced to 0600. The intent of
// this backend is "secrets live outside the version-controlled spec
// directory, behind the running user's file-system ACL." Stronger
// at-rest protection is a separate backend that this package's
// follow-up may add — the Store interface does not change.
package secret

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ErrNotFound is returned by Store.Get when no value is registered
// for the requested key. Distinct from a generic I/O error so the
// resolver (and the operator's CLI) can surface "missing key" as a
// specific, actionable diagnostic.
type ErrNotFound struct{ Key string }

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("secret %q not found in store", e.Key)
}

// Store is the contract every secret backend satisfies. Read-only
// from newtron-server's perspective; mutation methods (Set, Delete,
// List) exist so the operator-facing CLI (cmd/newtron secrets) can
// manage the store without operators editing JSON by hand. A backend
// that's intentionally read-only (e.g., a hosted KMS) may return a
// "not supported" error from Set/Delete; List is read-only and must
// always be implemented.
//
// All methods are safe for concurrent use.
type Store interface {
	// Get returns the value for key, or *ErrNotFound when the key
	// is not present. Other errors indicate backend I/O failures
	// (file system, network) and are propagated as-is.
	Get(key string) (string, error)

	// Set writes key → value. Overwrites any existing value at the
	// same key. Atomicity is the backend's responsibility — for
	// FileStore, writes go through a temp-file-rename sequence so a
	// crash during write doesn't leave the store half-updated.
	Set(key, value string) error

	// Delete removes the key. Returns *ErrNotFound if the key isn't
	// present, so a CLI invocation can distinguish "removed" from
	// "wasn't there to begin with."
	Delete(key string) error

	// List returns every key currently registered, sorted
	// lexicographically. Values are not returned — they should only
	// flow through Get on demand so a typo'd query doesn't print
	// every secret to the operator's terminal.
	List() ([]string, error)
}

// FileStore is a Store backed by a JSON file at Path. The file holds
// a flat map[string]string. File permissions are enforced to 0600
// (read/write by owner only) on every write, and Open refuses to
// proceed if the existing file is group- or world-readable — a
// stricter check than the kernel's default umask, on the principle
// that a secret store with broken permissions is worth refusing to
// open rather than silently leaking.
//
// Atomicity: Set/Delete write the new state to "Path.tmp" and rename
// over Path. A crash between write and rename leaves either the old
// file or the new file in place; the rename is atomic on POSIX file
// systems for paths on the same mount.
type FileStore struct {
	Path string

	mu sync.Mutex
}

// NewFileStore opens or initializes a FileStore at path. If the file
// doesn't exist, it's created with mode 0600 and an empty map. If
// it exists, the file mode is checked — modes broader than 0600 are
// rejected so a misconfigured permissions setup is loud rather than
// silent. The returned *FileStore is ready for concurrent use.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("secret: file store path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("secret: resolve path %q: %w", path, err)
	}
	s := &FileStore{Path: abs}

	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		// Create the file with an empty map so subsequent Gets
		// return a clean ErrNotFound rather than a confusing
		// "file doesn't exist" message at every lookup.
		if err := s.write(map[string]string{}); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secret: stat %q: %w", abs, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("secret: refusing to open %q: mode %v allows group/other access; chmod 0600 first", abs, mode)
	}
	// Verify the file is readable + parseable up front so the
	// operator finds out about corruption at startup, not at the
	// first secret lookup.
	if _, err := s.read(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get implements Store.
func (s *FileStore) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.read()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", &ErrNotFound{Key: key}
	}
	return v, nil
}

// Set implements Store.
func (s *FileStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.read()
	if err != nil {
		return err
	}
	m[key] = value
	return s.write(m)
}

// Delete implements Store.
func (s *FileStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return &ErrNotFound{Key: key}
	}
	delete(m, key)
	return s.write(m)
}

// List implements Store.
func (s *FileStore) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.read()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// read loads the current file contents into a map. Returns an empty
// map for a missing or empty file — the on-disk file is the source
// of truth and the empty case is valid (a fresh store).
func (s *FileStore) read() (map[string]string, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("secret: open %q: %w", s.Path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("secret: read %q: %w", s.Path, err)
	}
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("secret: parse %q: %w", s.Path, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// write serializes m to Path atomically via tmp+rename. Permissions
// on the tmp file are 0600 from the start so a concurrent reader
// cannot find a more-permissive temp file mid-write.
func (s *FileStore) write(m map[string]string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("secret: marshal: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.Path)
	// Ensure the parent directory exists for a first-time install
	// where the operator-supplied path is inside a directory they
	// haven't created yet.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("secret: mkdir %q: %w", dir, err)
	}

	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("secret: write tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secret: rename %q → %q: %w", tmp, s.Path, err)
	}
	return nil
}
