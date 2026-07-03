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

// TestNewNetwork_SecretRefInNodeSpecResolves pins the L0 end-to-end:
// a nodeSpec with ssh_pass="${secret:KEY}" and a store containing
// KEY=value loads cleanly; ResolveNodeSpec yields plaintext.
func TestNewNetwork_SecretRefInNodeSpecResolves(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeNodeSpec(t, dir, "switch1", `{
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

	n, err := NewNetwork(dir, "", nil, store, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("ResolveNodeSpec: %v", err)
	}
	if resolved.SSHPass != "the-real-password" {
		t.Errorf("SSHPass = %q; want the-real-password", resolved.SSHPass)
	}
}

// TestResolveNodeSpec_PlatformCredentialsFallback pins that a node which omits
// ssh_pass inherits the platform's credentials.pass — mirroring newtlab's
// NodeConfig resolution so newtron and newtlab reach a device with the same
// login. This is what lets a lab node authored with just a platform (e.g. one
// created through newtcon, which sets no ssh_pass) be reachable by newtron.
func TestResolveNodeSpec_PlatformCredentialsFallback(t *testing.T) {
	dir := t.TempDir()
	writeNetwork(t, dir)
	writeTopology(t, dir, "switch1")
	// The newtcon create shape: only ssh_user + platform, no ssh_pass.
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"underlay_asn": 65001
	}`)

	platforms := map[string]*spec.PlatformSpec{
		"p1": {Name: "p1", Credentials: &spec.Credentials{User: "aldrin", Pass: "YourPaSsWoRd"}},
	}
	n, err := NewNetwork(dir, "", nil, nil, platforms)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("resolveNodeSpec: %v", err)
	}
	if resolved.SSHPass != "YourPaSsWoRd" {
		t.Errorf("SSHPass = %q; want the platform credentials pass (YourPaSsWoRd)", resolved.SSHPass)
	}
	if resolved.SSHUser != "admin" {
		t.Errorf("SSHUser = %q; want admin", resolved.SSHUser)
	}
}

// TestResolveNodeSpec_NodeSSHPassOverridesPlatform pins that an explicit node
// ssh_pass wins over the platform default — the platform is only a fallback.
func TestResolveNodeSpec_NodeSSHPassOverridesPlatform(t *testing.T) {
	dir := t.TempDir()
	writeNetwork(t, dir)
	writeTopology(t, dir, "switch1")
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "node-pw",
		"underlay_asn": 65001
	}`)

	platforms := map[string]*spec.PlatformSpec{
		"p1": {Name: "p1", Credentials: &spec.Credentials{User: "aldrin", Pass: "platform-pw"}},
	}
	n, err := NewNetwork(dir, "", nil, nil, platforms)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("resolveNodeSpec: %v", err)
	}
	if resolved.SSHPass != "node-pw" {
		t.Errorf("SSHPass = %q; want node-pw (explicit node value wins over platform)", resolved.SSHPass)
	}
}

// TestNetwork_SetSecret_CreatesStoreAndResolves pins the API/UI half of the
// ${secret:KEY} design: SetSecret on a network that has no store yet creates
// <specDir>/secrets.json and adopts it, so the referenced value resolves in the
// live network without a reload — the flow newtcon uses to populate a credential
// it collected in a masked (secret:"true") input.
func TestNetwork_SetSecret_CreatesStoreAndResolves(t *testing.T) {
	dir := t.TempDir()
	writeNetwork(t, dir)
	writeTopology(t, dir, "switch1")
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "${secret:switch1_ssh_pass}",
		"underlay_asn": 65001
	}`)

	// No secrets.json, no explicit store — resolution is the disabled state.
	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	if _, err := n.resolveNodeSpecByName("switch1"); err == nil {
		t.Fatal("expected resolve to fail before the secret is set (reference, no store)")
	}

	// Set the secret through the API path — this must create <dir>/secrets.json.
	if err := n.SetSecret("switch1_ssh_pass", "s3cr3t"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "secrets.json")); statErr != nil {
		t.Errorf("secrets.json not created by SetSecret: %v", statErr)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("resolve after SetSecret: %v", err)
	}
	if resolved.SSHPass != "s3cr3t" {
		t.Errorf("SSHPass = %q, want s3cr3t (the value just set)", resolved.SSHPass)
	}

	// ListSecrets (the §24 read) returns the key — never the value.
	keys, err := n.ListSecrets()
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(keys) != 1 || keys[0] != "switch1_ssh_pass" {
		t.Errorf("ListSecrets = %v, want [switch1_ssh_pass]", keys)
	}

	// DeleteSecret is the reverse (§15) — succeeds against the now-present store,
	// and the key is gone from the listing.
	if err := n.DeleteSecret("switch1_ssh_pass"); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	keys, err = n.ListSecrets()
	if err != nil {
		t.Fatalf("ListSecrets after delete: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("ListSecrets after delete = %v, want empty", keys)
	}
}

// TestNetwork_DeleteSecret_Idempotent pins that deleting a key that isn't present
// — or from a network with no store at all — is a no-op, not an error (§13, like
// RemoveSuperUser; avoids a 500 for an already-satisfied delete).
func TestNetwork_DeleteSecret_Idempotent(t *testing.T) {
	dir := t.TempDir()
	writeNetwork(t, dir)
	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	// No store at all → no-op.
	if err := n.DeleteSecret("nope"); err != nil {
		t.Errorf("DeleteSecret with no store = %v; want nil (idempotent)", err)
	}
	// Store exists but the key doesn't → no-op.
	if err := n.SetSecret("present", "v"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if err := n.DeleteSecret("absent"); err != nil {
		t.Errorf("DeleteSecret of an absent key = %v; want nil (idempotent)", err)
	}
}

// TestNewNetwork_SecretRefWithoutStoreErrors pins the disabled-state
// behavior: a nodeSpec with a reference but no store configured fails
// at network load (not at first SSH attempt) — the operator sees the
// problem immediately on server startup, not under load.
func TestNewNetwork_SecretRefWithoutStoreErrors(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "${secret:switch1-ssh}",
		"underlay_asn": 65001
	}`)

	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v (the reference is only resolved on nodeSpec read; init should succeed)", err)
	}
	_, err = n.resolveNodeSpecByName("switch1")
	if err == nil {
		t.Fatal("expected ResolveNodeSpec to fail with no store + reference; got nil")
	}
	if !strings.Contains(err.Error(), "secret-store") {
		t.Errorf("err = %v; should mention --secret-store so operator knows the fix", err)
	}
}

// TestNewNetwork_PlaintextNodeSpecPassesThrough pins the no-regression
// path: a nodeSpec with plaintext ssh_pass loads with no store
// configured (current behavior), and the plaintext flows through
// ResolveNodeSpec unchanged.
func TestNewNetwork_PlaintextNodeSpecPassesThrough(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "YourPaSsWoRd",
		"underlay_asn": 65001
	}`)

	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("ResolveNodeSpec: %v", err)
	}
	if resolved.SSHPass != "YourPaSsWoRd" {
		t.Errorf("SSHPass = %q; want plaintext passthrough YourPaSsWoRd", resolved.SSHPass)
	}
}

// TestNewNetwork_SecretRefInPlatformResolves pins the platform path:
// a credentials.pass = "${secret:KEY}" reference resolves at the
// global-platforms load step (ResolvePlatformSecrets) so every
// Network sees plaintext.
func TestResolvePlatformSecrets_Resolves(t *testing.T) {
	store, err := secret.NewFileStore(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Set("p1-pass", "real-platform-pass"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	platforms := map[string]*spec.PlatformSpec{
		"p1": {
			Name:        "p1",
			Credentials: &spec.Credentials{User: "admin", Pass: "${secret:p1-pass}"},
		},
	}
	if err := ResolvePlatformSecrets(platforms, store); err != nil {
		t.Fatalf("ResolvePlatformSecrets: %v", err)
	}
	if got := platforms["p1"].Credentials.Pass; got != "real-platform-pass" {
		t.Errorf("platform pass = %q; want real-platform-pass (resolved from store)", got)
	}
}

// TestResolvePlatformSecrets_MissingKeyErrors pins that a platform
// reference with no matching store key fails fast — operators
// see the misconfiguration at server startup, not under load.
func TestResolvePlatformSecrets_MissingKeyErrors(t *testing.T) {
	store, err := secret.NewFileStore(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	platforms := map[string]*spec.PlatformSpec{
		"p1": {
			Name:        "p1",
			Credentials: &spec.Credentials{User: "admin", Pass: "${secret:nope}"},
		},
	}
	err = ResolvePlatformSecrets(platforms, store)
	if err == nil {
		t.Fatal("expected ResolvePlatformSecrets to fail with missing secret; got nil")
	}
	var nf *secret.ErrNotFound
	if !errors.As(err, &nf) {
		t.Errorf("err = %v; want *secret.ErrNotFound to be wrapped", err)
	}
}

// ============================================================================
// Fixture helpers — minimal spec layout sufficient to exercise the
// secret resolver in NewNetwork + ResolveNodeSpec.
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
		"version": "1.0"
	}`), 0o644); err != nil {
		t.Fatalf("write network: %v", err)
	}
	// Zones are per-file now (zones/<name>.json): the "amer" zone is an empty
	// override bucket declared by an empty file.
	if err := os.MkdirAll(filepath.Join(dir, "zones"), 0o755); err != nil {
		t.Fatalf("mkdir zones: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "zones", "amer.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write zone amer: %v", err)
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
		"nodes": {"` + deviceName + `": {}},
		"links": []
	}`
	if err := os.WriteFile(filepath.Join(dir, "topology.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}
}

func writeNodeSpec(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0o755); err != nil {
		t.Fatalf("mkdir nodeSpecs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nodes", name+".json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}
}

// resolveNodeSpecByName is a test-only convenience that loads + resolves
// in one call. Production code paths reach the same logic through
// Network.ConnectDevice / NewNode internally.
func (n *Network) resolveNodeSpecByName(name string) (*spec.ResolvedNodeSpec, error) {
	p, err := n.loadNodeSpec(name)
	if err != nil {
		return nil, err
	}
	return n.resolveNodeSpec(name, p)
}

func plainSwitch1NodeSpec() string {
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

// TestNewNetwork_SpecDirSecretStoreAutoDiscovery pins the #176
// convention: when the operator passes secretStore=nil AND
// <specDir>/secrets.json exists, the loader auto-opens it as a
// FileStore and resolves references against it. No --secret-store
// flag, no explicit operator config required.
//
// This is what keeps the README quickstart's `bin/newt-server` command
// unchanged after migrating test-topology nodeSpecs from plaintext to
// ${secret:KEY} references: the secrets.json sits next to network.json
// and is picked up automatically.
//
// §16: real on-disk network dir, real FileStore creation, real
// NewNetwork pass — no layer stubbed. The assertion targets the
// resolved password value end-to-end.
func TestNewNetwork_SpecDirSecretStoreAutoDiscovery(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "${secret:switch1-ssh}",
		"underlay_asn": 65001
	}`)

	// Drop secrets.json next to network.json with the canonical
	// mode 0600. The loader's convention discovers it.
	secretsPath := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(secretsPath, []byte(`{"switch1-ssh":"the-real-password"}`), 0o600); err != nil {
		t.Fatalf("write secrets.json: %v", err)
	}

	n, err := NewNetwork(dir, "", nil, nil, nil) // nil store → auto-discovery kicks in
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("ResolveNodeSpec: %v", err)
	}
	if resolved.SSHPass != "the-real-password" {
		t.Errorf("SSHPass = %q; want the-real-password (auto-discovery should have resolved it)", resolved.SSHPass)
	}
}

// TestNewNetwork_ExplicitStoreWinsOverSpecDirAutoDiscovery pins the
// precedence rule: an explicit secretStore argument always wins,
// even when <specDir>/secrets.json exists. Matches the established
// "flag wins over env" pattern from #179 (TLS env vars).
func TestNewNetwork_ExplicitStoreWinsOverSpecDirAutoDiscovery(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "${secret:switch1-ssh}",
		"underlay_asn": 65001
	}`)

	// spec-dir secrets.json says "loser".
	if err := os.WriteFile(filepath.Join(dir, "secrets.json"), []byte(`{"switch1-ssh":"loser"}`), 0o600); err != nil {
		t.Fatalf("write secrets.json: %v", err)
	}
	// Explicit store says "winner".
	explicit, err := secret.NewFileStore(filepath.Join(t.TempDir(), "explicit.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := explicit.Set("switch1-ssh", "winner"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	n, err := NewNetwork(dir, "", nil, explicit, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("ResolveNodeSpec: %v", err)
	}
	if resolved.SSHPass != "winner" {
		t.Errorf("SSHPass = %q; explicit store should have won over spec-dir auto-discovery", resolved.SSHPass)
	}
}

// TestNewNetwork_NoAutoDiscoveryWhenSecretsJsonAbsent pins the
// no-regression path: when secretStore=nil AND <specDir>/secrets.json
// doesn't exist, the loader proceeds as today (nil store; plaintext
// nodeSpecs work; references error at resolve time). The auto-discovery
// is strictly additive — it cannot break a setup that didn't have a
// secrets.json.
func TestNewNetwork_NoAutoDiscoveryWhenSecretsJsonAbsent(t *testing.T) {
	dir := newL0FixtureSpecDir(t)
	// Plaintext nodeSpec, no secrets.json.
	writeNodeSpec(t, dir, "switch1", `{
		"mgmt_ip": "127.0.0.1",
		"loopback_ip": "10.0.0.1",
		"zone": "amer",
		"platform": "p1",
		"ssh_user": "admin",
		"ssh_pass": "YourPaSsWoRd",
		"underlay_asn": 65001
	}`)

	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	resolved, err := n.resolveNodeSpecByName("switch1")
	if err != nil {
		t.Fatalf("ResolveNodeSpec: %v", err)
	}
	if resolved.SSHPass != "YourPaSsWoRd" {
		t.Errorf("SSHPass = %q; plaintext should pass through unchanged", resolved.SSHPass)
	}
}
