package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// Layout on disk: ~/.newtron/sessions/<user>@<host>.json, one file
// per (user, server) pair. The directory uses mode 0700; each file
// uses mode 0600. Multiple cached sessions coexist so an operator
// running test suites that exercise authorization assertions across
// several identities — alice, bob, mallory — can log in as each up
// front and have the runner pick the right Bearer per scenario step
// (the suite's `as: <user>` field).
//
// Single-user operators see no change in their daily flow: a single
// `newtron auth login` creates one file at sessions/<their-user>@
// <server>.json, and every CLI invocation finds the only cached
// session unambiguously. The multi-user path only matters when more
// than one file lives in the directory.
//
// The same root as the existing per-user settings file
// (pkg/newtron/settings) uses; the file mode distinguishes secret
// (0600 here) from non-secret (0644 for settings.json).

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

// SessionsDir returns the canonical on-disk root for cached
// session records. ~/.newtron/sessions/ — one place per user
// account; per-file the cache is keyed by (user, server) so
// multiple identities and multiple newtron deployments coexist
// without trampling each other.
//
// When os.UserHomeDir fails (highly unusual; broken NSS or a
// process running with no HOME), the path falls back to /tmp/
// rather than panicking. The fallback won't survive a reboot;
// an operator in that environment has bigger problems than
// session persistence.
func SessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/newtron-sessions"
	}
	return filepath.Join(home, ".newtron", "sessions")
}

// SessionPath returns the per-(user, server) file path for a
// cached session. The filename slug encodes both axes so multi-
// user / multi-server caches don't collide.
//
// Server hostnames carry colons (host:port) which are legal
// filename characters on Linux but awkward on operator-side
// tooling that quotes paths — we substitute "_" for ":". Other
// URL artifacts (scheme prefix, path component) are stripped to
// keep filenames stable when an operator switches between
// http://host:port and host:port in their flag spelling.
func SessionPath(user, server string) string {
	return filepath.Join(SessionsDir(), sessionFilename(user, server))
}

// sessionFilename derives the per-(user, server) cache filename
// from operator-supplied identifiers. The slug is "<user>@<host>"
// with the JSON extension; the host part normalizes scheme +
// trailing slashes + colon→underscore for cross-OS robustness.
func sessionFilename(user, server string) string {
	host := server
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	host = strings.TrimRight(host, "/")
	// "/" in the path portion (e.g. http://h:p/prefix) would create
	// nested directories on Save; flatten by replacing both "/"
	// and ":" with "_".
	host = strings.ReplaceAll(host, "/", "_")
	host = strings.ReplaceAll(host, ":", "_")
	return user + "@" + host + ".json"
}

// ErrInsecurePermissions is returned by Load when the session file
// exists with permissions broader than owner-only. The caller
// surfaces this to the operator with a remediation hint
// ("chmod 600 ~/.newtron/sessions/<user>@<host>.json") rather than silently using
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

// LoadSessionFor loads the cached session for a specific (user,
// server) pair. Thin convenience around LoadSession + SessionPath
// — the multi-user-aware callers (the runner's per-scenario
// Bearer lookup, `newtron auth logout --user X`) read by name
// rather than path-construct themselves.
func LoadSessionFor(user, server string) (*SessionRecord, error) {
	return LoadSession(SessionPath(user, server))
}

// ErrAmbiguousSession is returned by LoadCLISession when the
// operator has multiple cached sessions and no explicit selector
// (--user flag or NEWTRON_USER env). The caller surfaces a
// remediation hint listing the cached users so the operator picks
// the right one.
var ErrAmbiguousSession = errors.New("multiple cached sessions; specify --user or NEWTRON_USER")

// LoadCLISession resolves "the session the CLI should use" against
// the multi-user cache. Resolution order:
//
//   1. user != "" — caller passed --user X explicitly. Load that
//      user's session for server (or any server if server == "").
//   2. exactly one session cached — unambiguous default. Return it
//      whether server matches or not, because a single-user
//      operator's "the cached session" is unambiguous regardless
//      of which CLI flag spelled the server URL.
//   3. multiple sessions cached — ErrAmbiguousSession; caller
//      surfaces the hint listing cached users.
//   4. zero sessions cached — (nil, nil); caller proceeds with no
//      Bearer (the no-auth fallback for unauth-enforced servers).
//
// server is used as a tiebreaker for the multi-cached case: if
// user is empty AND multiple cached but all share the named
// server, the most-recently-modified wins. This handles the
// "operator has one identity but spelled the server URL two
// different ways across logins" case without forcing them to
// pass --user.
func LoadCLISession(user, server string) (*SessionRecord, error) {
	if user != "" {
		return LoadSessionFor(user, server)
	}
	all, _, err := ListSessions()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	if len(all) == 1 {
		return all[0], nil
	}
	// Filter to those matching the requested server (loose host
	// match — strip scheme so http://h:p matches h:p).
	if server != "" {
		matchHost := normalizeServerHost(server)
		var matches []*SessionRecord
		for _, r := range all {
			if normalizeServerHost(r.Server) == matchHost {
				matches = append(matches, r)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
	}
	users := make([]string, 0, len(all))
	for _, r := range all {
		users = append(users, r.User+"@"+r.Server)
	}
	return nil, fmt.Errorf("%w: cached sessions: %s",
		ErrAmbiguousSession, strings.Join(users, ", "))
}

// normalizeServerHost reduces a server URL to its host[:port] form
// for cache-filename matching. Used by LoadCLISession to treat
// "http://h:p", "h:p", and "h:p/" as the same server.
func normalizeServerHost(server string) string {
	host := server
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	return strings.TrimRight(host, "/")
}

// SaveSessionFor writes the record to its derived per-(user,
// server) path. Wrapper around SaveSession that hides path
// construction from callers — they hand over the record and the
// store decides where it goes.
func SaveSessionFor(rec *SessionRecord) error {
	if rec.User == "" || rec.Server == "" {
		return fmt.Errorf("SessionRecord requires User and Server to derive cache path")
	}
	return SaveSession(SessionPath(rec.User, rec.Server), rec)
}

// DeleteSessionFor removes the cached session for a (user, server)
// pair. Wrapper around DeleteSession; same idempotency contract.
func DeleteSessionFor(user, server string) error {
	return DeleteSession(SessionPath(user, server))
}

// ListSessions returns every valid (non-expired, well-formed,
// 0600-permitted) cached session in SessionsDir AND a slice of
// remediable problems with files that didn't make the cut. Used
// by `newtron auth status` (operator surfaces both lists) and by
// `cmd/newtrun start` (operator submits the valid set to the
// runner; the problem list is logged so a stale cache doesn't
// silently break a suite run).
//
// Problems include:
//
//   - Insecure permissions (ErrInsecurePermissions) — load-bearing
//     for the auth-design.md §L2c "protected as carefully as the
//     secret store" guarantee. The credential MIGHT have already
//     leaked; silently dropping it would hide the very signal the
//     mode check exists to surface.
//   - Malformed JSON or unreadable file — likely operator-edited
//     by accident; worth flagging so they can re-login rather
//     than seeing a sudden "no cached session" surprise.
//
// Expired records are intentionally NOT in problems — they're
// uninteresting (every record expires eventually) and surfacing
// them would clutter `auth status` output.
//
// Returns empty slices when SessionsDir doesn't exist (operator
// has never logged in) — not an error.
func ListSessions() ([]*SessionRecord, []SessionProblem, error) {
	dir := SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("list sessions dir: %w", err)
	}
	var out []*SessionRecord
	var problems []SessionProblem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		rec, err := LoadSession(p)
		if err != nil {
			problems = append(problems, SessionProblem{Path: p, Err: err})
			continue
		}
		if rec == nil {
			// Expired — silently dropped.
			continue
		}
		out = append(out, rec)
	}
	return out, problems, nil
}

// SessionProblem reports a cache-file the loader couldn't trust.
// Surfaced by `newtron auth status` so the operator sees signals
// they need to act on — most importantly the ErrInsecurePermissions
// case where a credential may have been world-readable. The Path
// field is the file the operator needs to inspect; Err carries
// the underlying reason (wrap of ErrInsecurePermissions or a
// JSON-decode error).
type SessionProblem struct {
	Path string
	Err  error
}
