package network

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestNewNetwork_SecretRefInProfileResolves pins the L0 end-to-end:
// a profile with ssh_pass="${secret:KEY}" and a store containing
// KEY=value loads cleanly; ResolveProfile yields plaintext.
func TestNewNetwork_SecretRefInProfileResolves(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeProfile(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "${secret:switch1-ssh}",
		"underlay_asn": 65001
	}`)

	store, err := secret.NewFileStore(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Set("switch1-ssh", "the-real-password"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	n, err := NewNetwork(dir, "", nil, store)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveProfileByName("switch1")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if resolved.SSHPass != "the-real-password" {
		t.Errorf("SSHPass = %q; want the-real-password", resolved.SSHPass)
	}
}

// TestNewNetwork_SecretRefWithoutStoreErrors pins the disabled-state
// behavior: a profile with a reference but no store configured fails
// at network load (not at first SSH attempt) — the operator sees the
// problem immediately on server startup, not under load.
func TestNewNetwork_SecretRefWithoutStoreErrors(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeProfile(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "${secret:switch1-ssh}",
		"underlay_asn": 65001
	}`)

	n, err := NewNetwork(dir, "", nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v (the reference is only resolved on profile read; init should succeed)", err)
	}
	_, err = n.resolveProfileByName("switch1")
	if err == nil {
		t.Fatal("expected ResolveProfile to fail with no store + reference; got nil")
	}
	if !strings.Contains(err.Error(), "secret-store") {
		t.Errorf("err = %v; should mention --secret-store so operator knows the fix", err)
	}
}

// TestNewNetwork_PlaintextProfilePassesThrough pins the no-regression
// path: a profile with plaintext ssh_pass loads with no store
// configured (current behavior), and the plaintext flows through
// ResolveProfile unchanged.
func TestNewNetwork_PlaintextProfilePassesThrough(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeProfile(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "YourPaSsWoRd",
		"underlay_asn": 65001
	}`)

	n, err := NewNetwork(dir, "", nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveProfileByName("switch1")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if resolved.SSHPass != "YourPaSsWoRd" {
		t.Errorf("SSHPass = %q; want plaintext passthrough YourPaSsWoRd", resolved.SSHPass)
	}
}

// TestNewNetwork_SecretRefInPlatformResolves pins the platform path:
// a vm_credentials.pass = "${secret:KEY}" reference in platforms.json
// resolves at network load so cached n.Platforms() carries plaintext.
func TestNewNetwork_SecretRefInPlatformResolves(t *testing.T) {
	dir := t.TempDir()
	writeNetwork(t, dir)
	writeTopology(t, dir, "switch1")
	writeProfile(t, dir, "switch1", plainSwitch1Profile())
	writePlatforms(t, dir, `{
		"version": "1.0",
		"platforms": {
			"p1": {
				"vm_credentials": {"user": "admin", "pass": "${secret:p1-pass}"}
			}
		}
	}`)

	store, err := secret.NewFileStore(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Set("p1-pass", "real-platform-pass"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	n, err := NewNetwork(dir, "", nil, store)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	got := n.platforms.Platforms["p1"].VMCredentials.Pass
	if got != "real-platform-pass" {
		t.Errorf("platform pass = %q; want real-platform-pass (resolved from store)", got)
	}
}

// TestNewNetwork_SecretRefInPlatformMissingKeyErrors pins that a
// platform reference with no matching store key fails at load —
// matches the profile-side behavior so the operator finds both
// surfaces of misconfiguration at the same time.
func TestNewNetwork_SecretRefInPlatformMissingKeyErrors(t *testing.T) {
	dir := t.TempDir()
	writeNetwork(t, dir)
	writeTopology(t, dir, "switch1")
	writeProfile(t, dir, "switch1", plainSwitch1Profile())
	writePlatforms(t, dir, `{
		"version": "1.0",
		"platforms": {
			"p1": {
				"vm_credentials": {"user": "admin", "pass": "${secret:nope}"}
			}
		}
	}`)

	store, err := secret.NewFileStore(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_, err = NewNetwork(dir, "", nil, store)
	if err == nil {
		t.Fatal("expected NewNetwork to fail with missing platform secret; got nil")
	}
	var nf *secret.ErrNotFound
	if !errors.As(err, &nf) {
		t.Errorf("err = %v; want *secret.ErrNotFound to be wrapped", err)
	}
}

// ============================================================================
// Fixture helpers — minimal spec layout sufficient to exercise the
// secret resolver in NewNetwork + ResolveProfile.
// ============================================================================

func newL0FixtureSpecDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeNetwork(t, dir)
	writePlatforms(t, dir, `{
		"version": "1.0",
		"platforms": {
			"p1": {}
		}
	}`)
	writeTopology(t, dir, "switch1")
	return dir
}

func writeNetwork(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(`{
		"version": "1.0",
		"zones": {"amer": {}}
	}`), 0o644); err != nil {
		t.Fatalf("write network: %v", err)
	}
}

func writePlatforms(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "platforms.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write platforms: %v", err)
	}
}

func writeTopology(t *testing.T, dir string, deviceName string) {
	t.Helper()
	body := `{
		"version": "1.0",
		"devices": {"` + deviceName + `": {}},
		"links": []
	}`
	if err := os.WriteFile(filepath.Join(dir, "topology.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}
}

func writeProfile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", name+".json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

// resolveProfileByName is a test-only convenience that loads + resolves
// in one call. Production code paths reach the same logic through
// Network.ConnectDevice / NewNode internally.
func (n *Network) resolveProfileByName(name string) (*spec.ResolvedProfile, error) {
	p, err := n.loadProfile(name)
	if err != nil {
		return nil, err
	}
	return n.resolveProfile(name, p)
}

func plainSwitch1Profile() string {
	return `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "YourPaSsWoRd",
		"underlay_asn": 65001
	}`
}
