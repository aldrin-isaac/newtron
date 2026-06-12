package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freshRecord builds a SessionRecord that won't expire during the
// test run. Reused across tests so individual cases focus on their
// specific assertion.
func freshRecord(t *testing.T) *SessionRecord {
	t.Helper()
	return &SessionRecord{
		Server:    "http://127.0.0.1:18080",
		User:      "alice",
		Key:       "test-session-key-abc",
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

// TestSaveLoad_RoundTrip pins the happy path: Save then Load
// returns the same record byte-for-byte equivalent (modulo time
// rounding through JSON which preserves to nanosecond precision).
func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	want := freshRecord(t)

	if err := SaveSession(path, want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got == nil {
		t.Fatal("LoadSession returned nil; want record")
	}
	if got.User != want.User || got.Key != want.Key || got.Server != want.Server {
		t.Errorf("loaded != saved: %+v vs %+v", got, want)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
}

// TestSaveSession_FileMode pins that the written file has mode
// 0600 — a credential file readable by anyone on the host would
// violate the auth-design.md §L2c protection guarantee that the
// cache is "as carefully protected as the secret store."
func TestSaveSession_FileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := SaveSession(path, freshRecord(t)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 0600", got)
	}
}

// TestLoadSession_MissingFile pins that a non-existent path
// returns (nil, nil) — the "operator hasn't logged in yet" case
// is NOT an error condition.
func TestLoadSession_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	got, err := LoadSession(path)
	if err != nil {
		t.Errorf("err = %v, want nil for missing file", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for missing file", got)
	}
}

// TestLoadSession_InsecurePermissions pins the security guard:
// a session file with mode broader than 0600 must NOT be silently
// used. The CLI surfaces the error so the operator can chmod or
// re-login rather than running with a credential anyone could
// have planted.
func TestLoadSession_InsecurePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := SaveSession(path, freshRecord(t)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	_, err := LoadSession(path)
	if err == nil {
		t.Fatal("err = nil, want ErrInsecurePermissions")
	}
	if !errors.Is(err, ErrInsecurePermissions) {
		t.Errorf("err = %v, want ErrInsecurePermissions", err)
	}
	// The message should name the file path so the operator can
	// chmod it directly without guessing where the cache lives.
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err message %q does not name the path %q", err.Error(), path)
	}
}

// TestLoadSession_Expired pins that an expired record returns
// (nil, nil) — the same shape as "no session." The CLI treats
// both identically: prompt for fresh credentials.
func TestLoadSession_Expired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	rec := freshRecord(t)
	rec.ExpiresAt = time.Now().Add(-time.Hour)
	if err := SaveSession(path, rec); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := LoadSession(path)
	if err != nil {
		t.Errorf("err = %v, want nil for expired", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for expired", got)
	}
}

// TestLoadSession_MalformedJSON pins that a corrupted file
// returns an error so the CLI can suggest "run `newtron auth
// login` again" rather than silently misbehaving.
func TestLoadSession_MalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := LoadSession(path)
	if err == nil {
		t.Fatal("err = nil, want decode error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want one mentioning decode", err)
	}
}

// TestSaveSession_AtomicWrite pins that a failure mid-stream
// leaves the previous session intact. Tested by verifying the
// temp file pattern: after a successful Save, the .tmp file
// must NOT linger.
func TestSaveSession_AtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := SaveSession(path, freshRecord(t)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session.json.tmp lingered after successful save: %v", err)
	}
}

// TestDeleteSession_Idempotent pins that DeleteSession on a
// missing file returns nil. The operator's intent ("there should
// be no cached session") is satisfied either way — matches the
// /auth/logout idempotency.
func TestDeleteSession_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := DeleteSession(path); err != nil {
		t.Errorf("Delete on missing file: %v", err)
	}

	if err := SaveSession(path, freshRecord(t)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := DeleteSession(path); err != nil {
		t.Errorf("Delete on existing file: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present after Delete: %v", err)
	}
}

// TestSaveSession_CreatesDirectory pins that SaveSession creates
// the parent directory when missing. First-time-ever-login
// operator otherwise hits "no such file or directory" on the
// fresh-system path.
func TestSaveSession_CreatesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "subdir", "session.json")
	if err := SaveSession(path, freshRecord(t)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not present after Save: %v", err)
	}
}

// TestSessionRecord_ExpiredHonorsClock pins the Expired check:
// a record whose ExpiresAt is in the future is NOT expired; one
// in the past IS. Edge cases around exact-now are intentionally
// permissive (clock skew swamps any precise instant).
func TestSessionRecord_ExpiredHonorsClock(t *testing.T) {
	future := &SessionRecord{ExpiresAt: time.Now().Add(time.Minute)}
	if future.Expired() {
		t.Error("future expiry reported as expired")
	}
	past := &SessionRecord{ExpiresAt: time.Now().Add(-time.Minute)}
	if !past.Expired() {
		t.Error("past expiry reported as not expired")
	}
}

// TestWithBearer_AttachesHeader pins the static-Bearer transport:
// every outbound request gets Authorization: Bearer <key>.
func TestWithBearer_AttachesHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "net-1", WithBearer("test-key"))
	if _, err := c.RawRequest("GET", "/newtron/v1/networks", nil); err != nil {
		t.Fatalf("RawRequest: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
}

// TestWithBearer_EmptyKeyNoOp pins that WithBearer("") leaves the
// transport untouched. The CLI calls WithBearer(record.Key)
// unconditionally and passes "" when there's no cached session;
// that path must not attach a malformed Bearer header.
func TestWithBearer_EmptyKeyNoOp(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "net-1", WithBearer(""))
	if _, err := c.RawRequest("GET", "/newtron/v1/networks", nil); err != nil {
		t.Fatalf("RawRequest: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty for empty key", gotAuth)
	}
}

// TestWithBearer_RespectsCallerAuthorization pins that an
// explicit Authorization header on a request passes through
// unchanged — needed for /auth/login (Basic) and /auth/logout
// (Bearer of a different key being revoked).
func TestWithBearer_RespectsCallerAuthorization(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "net-1", WithBearer("cached-key"))
	if _, err := c.RawRequest("POST", "/newtron/v1/auth/login", nil,
		WithHeader("Authorization", "Basic YWxpY2U6cHc=")); err != nil {
		t.Fatalf("RawRequest: %v", err)
	}
	if gotAuth != "Basic YWxpY2U6cHc=" {
		t.Errorf("Authorization = %q, want the caller's Basic header", gotAuth)
	}
}

// TestLoadSession_FollowsSymlinks pins that os.Stat-following
// behavior: a symlink pointing at a well-formed 0600 session
// file loads successfully. Documented as a deliberate choice
// (operators can use symlinks intentionally to point at e.g. a
// tmpfs-backed location) rather than a security guard. If
// symlink-attack surfaces ever matter for newtron's deployment
// shape, switch to os.Lstat plus an explicit non-symlink check
// and rename this test to its inverse.
func TestLoadSession_FollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.json")
	linkPath := filepath.Join(dir, "session.json")
	if err := SaveSession(realPath, freshRecord(t)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}
	rec, err := LoadSession(linkPath)
	if err != nil {
		t.Fatalf("LoadSession via symlink: %v", err)
	}
	if rec == nil {
		t.Fatal("LoadSession via symlink returned nil")
	}
}

// TestDefaultSessionPath_UsesNewtronRoot pins the convention
// that the session file lives in ~/.newtron/ alongside the
// existing settings.json. One root for all newtron client-side
// state; file modes (0600 vs 0644) distinguish secret from non-
// secret.
func TestDefaultSessionPath_UsesNewtronRoot(t *testing.T) {
	got := DefaultSessionPath()
	if !strings.Contains(got, ".newtron") {
		t.Errorf("DefaultSessionPath() = %q, want one containing .newtron", got)
	}
	if !strings.HasSuffix(got, "session.json") {
		t.Errorf("DefaultSessionPath() = %q, want one ending in session.json", got)
	}
}

// TestSessionRecord_JSONShape pins the wire format. Operators
// who inspect the file directly (a reasonable thing to do when
// debugging) see field names that match the auth-design.md L2c
// vocabulary — server, user, key, expires_at — not Go-side
// camelCase.
func TestSessionRecord_JSONShape(t *testing.T) {
	rec := freshRecord(t)
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	str := string(data)
	for _, want := range []string{`"server"`, `"user"`, `"key"`, `"expires_at"`} {
		if !strings.Contains(str, want) {
			t.Errorf("JSON %q missing field %s", str, want)
		}
	}
}
