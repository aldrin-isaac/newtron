package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestResolveProfileSecrets_PlaintextPassesThrough pins the §2.4
// disabled-state path: a profile whose SSHPass is a plain literal
// stays unchanged regardless of whether a store is configured.
func TestResolveProfileSecrets_PlaintextPassesThrough(t *testing.T) {
	p := &spec.NodeSpec{SSHUser: "admin", SSHPass: "literal-pass"}
	if err := resolveNodeSpecSecrets(p, nil); err != nil {
		t.Fatalf("resolveNodeSpecSecrets: %v", err)
	}
	if p.SSHUser != "admin" || p.SSHPass != "literal-pass" {
		t.Errorf("plaintext mutated: user=%q pass=%q", p.SSHUser, p.SSHPass)
	}
}

// TestResolveProfileSecrets_ReferenceResolves pins the happy path
// the 1node-vs-auth suite's L0 scenario surfaced: SSHPass = ${secret:KEY}
// becomes the literal stored value after resolution. Pre-fix, this
// step on the suite returned the unresolved ${secret:...} string.
func TestResolveProfileSecrets_ReferenceResolves(t *testing.T) {
	store := newFileStoreWith(t, map[string]string{"switch1_ssh_pass": "real-password"})
	p := &spec.NodeSpec{SSHUser: "admin", SSHPass: "${secret:switch1_ssh_pass}"}
	if err := resolveNodeSpecSecrets(p, store); err != nil {
		t.Fatalf("resolveNodeSpecSecrets: %v", err)
	}
	if p.SSHPass != "real-password" {
		t.Errorf("SSHPass = %q, want real-password", p.SSHPass)
	}
}

// TestResolveProfileSecrets_MissingStoreFailsClosed pins that a
// reference under a nil store is a hard error — the operator must
// see this immediately rather than discover via a failed SSH login.
func TestResolveProfileSecrets_MissingStoreFailsClosed(t *testing.T) {
	p := &spec.NodeSpec{SSHPass: "${secret:KEY}"}
	if err := resolveNodeSpecSecrets(p, nil); err == nil {
		t.Error("resolveNodeSpecSecrets succeeded on a reference with nil store; expected hard error")
	}
}

// TestResolveProfileSecrets_Idempotent pins that running resolution
// twice on the same in-memory profile is safe — the loader caches
// pointers, so Network.loadNodeSpec re-runs resolution on every
// access; idempotency must hold or cached profiles would corrupt.
func TestResolveProfileSecrets_Idempotent(t *testing.T) {
	store := newFileStoreWith(t, map[string]string{"k": "v"})
	p := &spec.NodeSpec{SSHPass: "${secret:k}"}
	for i := 0; i < 3; i++ {
		if err := resolveNodeSpecSecrets(p, store); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if p.SSHPass != "v" {
			t.Errorf("pass %d: SSHPass = %q, want v", i, p.SSHPass)
		}
	}
}

// TestResolveProfileSecrets_NilProfile pins the defensive no-op —
// callers should never hand us nil, but if they do we don't panic.
func TestResolveProfileSecrets_NilProfile(t *testing.T) {
	if err := resolveNodeSpecSecrets(nil, nil); err != nil {
		t.Errorf("nil profile: got error %v, want nil", err)
	}
}

// newFileStoreWith writes m to a temp file and opens it as a
// secret.FileStore. The helper hides the temp-file plumbing so the
// tests above read as "this map is the secret store".
func newFileStoreWith(t *testing.T, m map[string]string) secret.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.json")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := secret.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s
}
