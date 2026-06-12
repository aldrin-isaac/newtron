package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// File-backed user-session storage for the newtron CLI (the parallel
// to the in-memory daemon-side cache that pkg/newtron/api owns on
// the server). The cache lets an operator run `newtron auth login`
// once, then have every subsequent newtron / newtrun / newtlab
// invocation reuse the resulting session key without re-prompting
// for credentials.
//
// auth-design.md §L2c "Storage model" is explicit that *server-side*
// session-key state is in-memory by design (a restart should
// terminate sessions). It is silent on whether *clients* may
// persist; the L2c "Programmatic clients" subsection that motivates
// the layer assumes exactly the embed-a-key-on-disk pattern this
// file implements for human operators. The auth-design admonition
// that "persistence would introduce a credential file on disk that
// has to be protected as carefully as the secret store" applies
// directly here — the file is owner-readable only (0600) and lives
// under the user's $HOME, never world-readable, never in
// /etc-style locations.
//
// File path: ~/.newtron/session.json — the same root the existing
// per-user settings file (pkg/newtron/settings) uses. One root for
// all newtron client-side state on disk; the file mode distinguishes
// secret (0600 here) from non-secret (0644 for settings.json).

// SessionRecord is what we write to disk after a successful login.
// Server pins the cache to one newtron URL (a key minted against
// server A doesn't authenticate at server B); ExpiresAt lets the
// loader reject records past their stated TTL without sending a
// known-bad Bearer; User is for `newtron auth status` display so
// the operator can confirm identity without decoding the opaque
// key; Key is the opaque credential the round-tripper carries.
type SessionRecord struct {
	Server    string    `json:"server"`
	User      string    `json:"user"`
	Key       string    `json:"key"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the record's stated expiry has passed.
// The check is local: clock skew between client and server could
// move the answer by a few seconds in either direction, but a
// short-window skew on TTL is harmless because the server makes
// the final call at request time (returns 401 if the server-side
// store has already evicted).
func (r *SessionRecord) Expired() bool {
	return !time.Now().Before(r.ExpiresAt)
}

// DefaultSessionPath returns the canonical on-disk location of the
// user-session record. The same root as the existing per-user
// settings file — one place for all newtron client-side state per
// user account.
//
// When os.UserHomeDir fails (highly unusual; broken NSS or a
// process running with no HOME), the path falls back to /tmp/
// rather than panicking. The fallback location is owner-readable
// only via the file mode write — the directory's existing
// permissions are not assumed. A CLI that lands on /tmp/ will work
// but won't survive a reboot; an operator in that environment has
// bigger problems than session persistence.
func DefaultSessionPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/newtron-session.json"
	}
	return filepath.Join(home, ".newtron", "session.json")
}

// ErrInsecurePermissions is returned by Load when the session file
// exists with permissions broader than owner-only. The caller
// surfaces this to the operator with a remediation hint
// ("chmod 600 ~/.newtron/session.json") rather than silently using
// a credential anyone on the host could have tampered with.
var ErrInsecurePermissions = errors.New("session file permissions are not 0600")

// LoadSession reads the cached session from path. Returns
// (nil, nil) when:
//
//   - the file does not exist (operator has not logged in)
//   - the file exists but the recorded expiry has passed (logout
//     equivalent — caller should prompt for fresh credentials)
//
// Returns (nil, error) when:
//
//   - permissions are not 0600 (ErrInsecurePermissions; security
//     guard so a world-readable session file doesn't silently
//     succeed)
//   - the file is unreadable or malformed (caller surfaces with
//     "your session file is corrupted; run `newtron auth login`")
//
// The expiry check happens AFTER the permissions check on purpose:
// an insecure file is a security issue regardless of whether the
// key inside is expired.
func LoadSession(path string) (*SessionRecord, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat session file: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: got %o, want 0600 — chmod 600 %s",
			ErrInsecurePermissions, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}
	var rec SessionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decode session file: %w", err)
	}
	if rec.Expired() {
		return nil, nil
	}
	return &rec, nil
}

// SaveSession writes rec to path with mode 0600. Uses temp-then-
// rename so concurrent readers can never see a partially-written
// file (and a write that fails mid-stream leaves the previous
// session intact). The directory is created with 0700 if missing.
//
// SaveSession overwrites any prior session for this user — a
// second login replaces the cached key. There is no "merge" or
// "append" semantic; one user, one cached session, one
// (server, key) pair at a time.
func SaveSession(path string, rec *SessionRecord) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	tmp := path + ".tmp"
	// Mode is set at open-time so the file is owner-only readable
	// even during the brief temp window. os.WriteFile would set
	// the mode after the bytes were written, which is fine in
	// practice but the explicit OpenFile makes the security
	// guarantee on the close-and-rename path explicit.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open session tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write session tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close session tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename session: %w", err)
	}
	return nil
}

// DeleteSession removes the session file at path. Returns nil if
// the file is already absent — the operator's intent ("there
// should be no cached session") is satisfied either way, matching
// the logout idempotency of the server-side /auth/logout handler.
func DeleteSession(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove session file: %w", err)
	}
	return nil
}
