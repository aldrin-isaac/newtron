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
// cache is "protected as carefully as the secret store."
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
	if _, err := c.RawRequest("POST", "/newt-server/v1/auth/login", nil,
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

// TestSessionsDir_UsesNewtronRoot pins the convention that
// cached session files live under ~/.newtron/sessions/ alongside
// the existing settings.json — one root for all newtron client-
// side state per user account.
func TestSessionsDir_UsesNewtronRoot(t *testing.T) {
	got := SessionsDir()
	if !strings.Contains(got, ".newtron") {
		t.Errorf("SessionsDir() = %q, want one containing .newtron", got)
	}
	if !strings.HasSuffix(got, "sessions") {
		t.Errorf("SessionsDir() = %q, want one ending in sessions", got)
	}
}

// TestSessionPath_EncodesUserAndServer pins the filename
// encoding: <user>@<host>.json with scheme/colon/slash normalized
// so concurrent multi-user multi-server caches don't collide.
func TestSessionPath_EncodesUserAndServer(t *testing.T) {
	cases := []struct {
		user, server, wantFile string
	}{
		{"alice", "http://127.0.0.1:18080", "alice@127.0.0.1_18080.json"},
		{"bob", "https://newtron.example:443", "bob@newtron.example_443.json"},
		{"mallory", "127.0.0.1:18080", "mallory@127.0.0.1_18080.json"},
		{"intf-isaac", "http://h:p/prefix", "intf-isaac@h_p_prefix.json"},
	}
	for _, c := range cases {
		got := SessionPath(c.user, c.server)
		if filepath.Base(got) != c.wantFile {
			t.Errorf("SessionPath(%q, %q) basename = %q, want %q",
				c.user, c.server, filepath.Base(got), c.wantFile)
		}
	}
}

// TestLoadSessionFor_RoundTrip pins the user-keyed convenience
// wrappers: SaveSessionFor + LoadSessionFor produce the same
// record without the caller managing paths.
func TestLoadSessionFor_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := freshRecord(t)
	if err := SaveSessionFor(want); err != nil {
		t.Fatalf("SaveSessionFor: %v", err)
	}
	got, err := LoadSessionFor(want.User, want.Server)
	if err != nil {
		t.Fatalf("LoadSessionFor: %v", err)
	}
	if got == nil || got.Key != want.Key {
		t.Errorf("got %+v, want a record with key %s", got, want.Key)
	}
}

// TestSaveSessionFor_RejectsMissingUser pins the contract that
// the (user, server) pair are required — these are what derive
// the cache file path; saving without them would dump the record
// at sessions/@.json which is meaningless and noisy.
func TestSaveSessionFor_RejectsMissingUser(t *testing.T) {
	rec := &SessionRecord{Server: "x", Key: "k", ExpiresAt: time.Now().Add(time.Hour)}
	if err := SaveSessionFor(rec); err == nil {
		t.Error("expected error on empty User; got nil")
	}
	rec2 := &SessionRecord{User: "alice", Key: "k", ExpiresAt: time.Now().Add(time.Hour)}
	if err := SaveSessionFor(rec2); err == nil {
		t.Error("expected error on empty Server; got nil")
	}
}

// TestListSessions_EnumeratesValidOnly pins the contract that
// ListSessions returns only well-formed, unexpired, properly-
// permitted records. Garbage files (other JSONs, expired records,
// chmod-644 records) are skipped silently.
func TestListSessions_EnumeratesValidOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Two valid sessions:
	alice := &SessionRecord{User: "alice", Server: "http://h:1", Key: "ka", ExpiresAt: time.Now().Add(time.Hour)}
	bob := &SessionRecord{User: "bob", Server: "http://h:1", Key: "kb", ExpiresAt: time.Now().Add(time.Hour)}
	for _, r := range []*SessionRecord{alice, bob} {
		if err := SaveSessionFor(r); err != nil {
			t.Fatalf("SaveSessionFor: %v", err)
		}
	}
	// One expired:
	expired := &SessionRecord{User: "old", Server: "http://h:1", Key: "kx", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := SaveSessionFor(expired); err != nil {
		t.Fatalf("SaveSessionFor expired: %v", err)
	}
	// One stray non-session JSON file:
	stray := filepath.Join(SessionsDir(), "stray.json")
	if err := os.WriteFile(stray, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile stray: %v", err)
	}

	got, _, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	users := map[string]bool{}
	for _, r := range got {
		users[r.User] = true
	}
	if !users["alice"] || !users["bob"] {
		t.Errorf("missing valid sessions; got users %v", users)
	}
	if users["old"] {
		t.Error("ListSessions included an expired session")
	}
}

// TestListSessions_SurfacesInsecurePermissions pins the §L2c
// protection-guarantee path: a tampered (chmod-644) cache file
// must NOT be silently dropped from `auth status` output —
// silently hiding it would also hide the very signal the mode
// check exists to surface, allowing a leaked Bearer to disappear
// from the operator's view. The file appears in the problems
// list with ErrInsecurePermissions, the valid sessions list
// excludes it.
func TestListSessions_SurfacesInsecurePermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	good := &SessionRecord{User: "alice", Server: "http://h:1", Key: "ka", ExpiresAt: time.Now().Add(time.Hour)}
	bad := &SessionRecord{User: "bob", Server: "http://h:1", Key: "kb", ExpiresAt: time.Now().Add(time.Hour)}
	for _, r := range []*SessionRecord{good, bad} {
		if err := SaveSessionFor(r); err != nil {
			t.Fatalf("SaveSessionFor: %v", err)
		}
	}
	// Tamper with bob's file mode after the save.
	if err := os.Chmod(SessionPath("bob", "http://h:1"), 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	valid, problems, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(valid) != 1 || valid[0].User != "alice" {
		t.Errorf("valid sessions = %v, want one alice", valid)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %d, want 1", len(problems))
	}
	if !errors.Is(problems[0].Err, ErrInsecurePermissions) {
		t.Errorf("problem.Err = %v, want ErrInsecurePermissions", problems[0].Err)
	}
	if !strings.Contains(problems[0].Path, "bob") {
		t.Errorf("problem.Path = %q, want one mentioning bob", problems[0].Path)
	}
}

// TestListSessions_MissingDirIsEmpty pins the "operator has
// never logged in" path: SessionsDir doesn't exist → empty slice,
// no error.
func TestListSessions_MissingDirIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, _, err := ListSessions()
	if err != nil {
		t.Errorf("err = %v, want nil for missing dir", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d records, want 0", len(got))
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

// TestResolveCLIBearer pins the single owner of "the Bearer a CLI presents"
// (§27). The load-bearing case is NEWTRON_BEARER precedence: a forwarded
// identity must win over — and never be blocked by — whatever the on-disk
// cache holds, including an otherwise-fatal ambiguous cache. This is the
// behavior that previously lived in cmd/newtron only; every CLI now shares it.
func TestResolveCLIBearer(t *testing.T) {
	const server = "http://127.0.0.1:18080"

	saveTwo := func(t *testing.T) {
		t.Helper()
		for _, u := range []string{"alice", "bob"} {
			rec := &SessionRecord{User: u, Server: server, Key: "key-" + u, ExpiresAt: time.Now().Add(time.Hour)}
			if err := SaveSessionFor(rec); err != nil {
				t.Fatalf("SaveSessionFor(%s): %v", u, err)
			}
		}
	}

	t.Run("NEWTRON_BEARER wins over the cache", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("NEWTRON_BEARER", "forwarded-key")
		if err := SaveSessionFor(&SessionRecord{User: "alice", Server: server, Key: "cached-key", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("SaveSessionFor: %v", err)
		}
		got, err := ResolveCLIBearer(server)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "forwarded-key" {
			t.Errorf("got %q, want the env key to win over the cache", got)
		}
	})

	t.Run("NEWTRON_BEARER short-circuits an ambiguous cache", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("NEWTRON_USER", "") // no selector → cache alone would be ambiguous
		t.Setenv("NEWTRON_BEARER", "forwarded-key")
		saveTwo(t)
		got, err := ResolveCLIBearer(server)
		if err != nil {
			t.Fatalf("env key must short-circuit before the ambiguous-cache error, got: %v", err)
		}
		if got != "forwarded-key" {
			t.Errorf("got %q, want forwarded-key", got)
		}
	})

	t.Run("falls back to the cache when NEWTRON_BEARER is unset", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("NEWTRON_BEARER", "")
		if err := SaveSessionFor(&SessionRecord{User: "alice", Server: server, Key: "cached-key", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("SaveSessionFor: %v", err)
		}
		got, err := ResolveCLIBearer(server)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "cached-key" {
			t.Errorf("got %q, want the cached key", got)
		}
	})

	t.Run("no env, no cache → empty (no-auth path)", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("NEWTRON_BEARER", "")
		got, err := ResolveCLIBearer(server)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty string (WithBearer no-op)", got)
		}
	})

	t.Run("no env, ambiguous cache, no selector → empty (no-op, not fatal)", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("NEWTRON_BEARER", "")
		t.Setenv("NEWTRON_USER", "")
		saveTwo(t)
		// An ambiguous cache must NOT be fatal — the CLI presents no Bearer so
		// the no-auth quickstart keeps working; an enforced server's 401 (or
		// NEWTRON_USER) is how a multi-identity operator disambiguates.
		got, err := ResolveCLIBearer(server)
		if err != nil {
			t.Fatalf("ambiguous cache must not error, got: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty (no single identity to present)", got)
		}
	})

	t.Run("NEWTRON_USER selects one identity out of several", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("NEWTRON_BEARER", "")
		t.Setenv("NEWTRON_USER", "bob")
		saveTwo(t)
		got, err := ResolveCLIBearer(server)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "key-bob" {
			t.Errorf("got %q, want key-bob (NEWTRON_USER disambiguates)", got)
		}
	})
}
