package secret

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolve_PlaintextPassthrough pins that values without the
// reference prefix are returned unchanged with no error — so
// existing plaintext spec fields keep working when --secret-store is
// not yet adopted.
func TestResolve_PlaintextPassthrough(t *testing.T) {
	// store is nil to also pin that no-store + plaintext is fine.
	got, err := Resolve("YourPaSsWoRd", nil)
	if err != nil {
		t.Errorf("err = %v; want nil for plaintext", err)
	}
	if got != "YourPaSsWoRd" {
		t.Errorf("Resolve plaintext = %q; want unchanged", got)
	}
}

// TestResolve_ReferenceLooksUp pins the happy path: a reference is
// recognized, the key is parsed, the store is consulted, the stored
// value is returned.
func TestResolve_ReferenceLooksUp(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "r.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s.Set("switch1-ssh", "the-real-password"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := Resolve("${secret:switch1-ssh}", s)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "the-real-password" {
		t.Errorf("Resolve ref = %q; want the-real-password", got)
	}
}

// TestResolve_RefWithoutStoreErrors pins the safety check: a
// reference in spec with no store configured is a hard error rather
// than the literal "${secret:KEY}" being sent through as a password.
// Silent fall-through would be a security regression — the spec
// opted in to the resolver path, so the absent resolver must fail
// loudly.
func TestResolve_RefWithoutStoreErrors(t *testing.T) {
	_, err := Resolve("${secret:switch1-ssh}", nil)
	if err == nil {
		t.Fatal("expected err for reference with nil store; got nil")
	}
	if !strings.Contains(err.Error(), "secret-store") {
		t.Errorf("err message %q should mention --secret-store so the operator knows the fix", err)
	}
}

// TestResolve_MissingKeyPropagates pins that a reference to a key
// not in the store surfaces *ErrNotFound carrying the key name.
func TestResolve_MissingKeyPropagates(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "m.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_, err = Resolve("${secret:nope}", s)
	if !isNotFound(err) {
		t.Errorf("err = %v; want *ErrNotFound", err)
	}
}

// TestResolve_EmptyKeyRejected pins that "${secret:}" — an empty key
// within the reference — is rejected as malformed rather than
// returning the literal empty value from the store. The operator
// almost certainly mistyped; surface it.
func TestResolve_EmptyKeyRejected(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "e.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if _, err := Resolve("${secret:}", s); err == nil {
		t.Error("expected err for empty-key reference; got nil")
	}
}

// TestIsRef pins the reference-detection contract used by callers
// that branch on the value type without resolving immediately
// (e.g., a future schema validator).
func TestIsRef(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"${secret:foo}", true},
		{"${secret:}", true}, // syntactically a ref; Resolve will reject the empty key
		{"YourPaSsWoRd", false},
		{"${secret:foo", false},  // missing close
		{"secret:foo}", false},   // missing prefix
		{"", false},
		{"$secret:foo", false}, // shell-style sigil only, no braces
	}
	for _, c := range cases {
		if got := IsRef(c.in); got != c.want {
			t.Errorf("IsRef(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}
