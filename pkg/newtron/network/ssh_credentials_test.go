package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// newSSHFixture builds a minimal on-disk network (network base ssh + one zone +
// one node that authors no login) and returns the loaded Network. The node
// inherits the network login until a scoped write overrides it — the substrate
// for exercising the scalar scope-write surface end to end.
func newSSHFixture(t *testing.T) *Network {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("network.json", `{"version":"1.0","ssh_user":"netuser","ssh_pass":"netpass"}`)
	mustWrite("zones/amer.json", `{}`)
	mustWrite("nodes/leaf1.json", `{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.1","zone":"amer","platform":"p1","underlay_asn":65001}`)
	mustWrite("topology.json", `{"version":"1.0","nodes":{"leaf1":{}},"links":[]}`)

	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	return n
}

// TestSSHCredentials_SetReadClearRoundTrip pins the scalar scope-write surface at
// all three scopes: a set is read back verbatim at the same scope (no hierarchy
// fallback), and a clear removes it. Also confirms a network-scope set actually
// feeds resolution — the effective login the device dials changes with it.
func TestSSHCredentials_SetReadClearRoundTrip(t *testing.T) {
	n := newSSHFixture(t)

	// Network scope: overwrite the base, confirm authored read + effective read.
	if err := n.SetSSHCredentials(spec.ScopeNetwork, "", "netuser2", "netpass2"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	got, err := n.GetSSHCredentialsAt(spec.ScopeNetwork, "")
	if err != nil {
		t.Fatalf("read network: %v", err)
	}
	if got.SSHUser != "netuser2" || got.SSHPass != "netpass2" {
		t.Errorf("network authored = %q/%q, want netuser2/netpass2", got.SSHUser, got.SSHPass)
	}
	// leaf1 authors nothing → inherits the new network base.
	if eff, err := n.resolveNodeSpecByName("leaf1"); err != nil {
		t.Fatalf("resolve leaf1: %v", err)
	} else if eff.SSHUser != "netuser2" || eff.SSHPass != "netpass2" {
		t.Errorf("effective leaf1 = %q/%q, want the network base netuser2/netpass2", eff.SSHUser, eff.SSHPass)
	}

	// Node scope: a node override wins over the network base.
	if err := n.SetSSHCredentials(spec.ScopeNode, "leaf1", "leafuser", "leafpass"); err != nil {
		t.Fatalf("set node: %v", err)
	}
	if eff, err := n.resolveNodeSpecByName("leaf1"); err != nil {
		t.Fatalf("resolve leaf1: %v", err)
	} else if eff.SSHUser != "leafuser" || eff.SSHPass != "leafpass" {
		t.Errorf("effective leaf1 = %q/%q, want the node override leafuser/leafpass", eff.SSHUser, eff.SSHPass)
	}

	// Clear the node override → leaf1 falls back to the network base again (§15).
	if err := n.ClearSSHCredentials(spec.ScopeNode, "leaf1"); err != nil {
		t.Fatalf("clear node: %v", err)
	}
	if got, err := n.GetSSHCredentialsAt(spec.ScopeNode, "leaf1"); err != nil {
		t.Fatalf("read node after clear: %v", err)
	} else if got.SSHUser != "" || got.SSHPass != "" {
		t.Errorf("node authored after clear = %q/%q, want empty", got.SSHUser, got.SSHPass)
	}
	if eff, err := n.resolveNodeSpecByName("leaf1"); err != nil {
		t.Fatalf("resolve leaf1 after clear: %v", err)
	} else if eff.SSHUser != "netuser2" {
		t.Errorf("effective leaf1 after clear = %q, want the network base netuser2 (fell back)", eff.SSHUser)
	}
}

// TestSSHCredentials_SecretRefPreservedInAuthoredRead pins that the authored read
// returns a ${secret:KEY} ssh_pass as the pointer it is — never resolved to
// plaintext — so an authoring UI can see which key is referenced. (The public
// ShowSSHCredentials masks any plaintext; the raw ref is not a secret value.)
func TestSSHCredentials_SecretRefPreservedInAuthoredRead(t *testing.T) {
	n := newSSHFixture(t)
	if err := n.SetSSHCredentials(spec.ScopeZone, "amer", "zoneuser", "${secret:amer_pass}"); err != nil {
		t.Fatalf("set zone: %v", err)
	}
	got, err := n.GetSSHCredentialsAt(spec.ScopeZone, "amer")
	if err != nil {
		t.Fatalf("read zone: %v", err)
	}
	if got.SSHPass != "${secret:amer_pass}" {
		t.Errorf("zone authored ssh_pass = %q, want the ${secret:} reference preserved verbatim", got.SSHPass)
	}
}

// TestSSHCredentials_NoNetworkFloor pins the design difference from map
// overridables: a node/zone login override needs NO network base (resolution
// falls back to platform/"admin"), so a scoped set never requires a network-scope
// login first — unlike checkOverrideBase for the map kinds.
func TestSSHCredentials_NoNetworkFloor(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Network authors NO login at all.
	mustWrite("network.json", `{"version":"1.0"}`)
	mustWrite("zones/amer.json", `{}`)
	mustWrite("nodes/leaf1.json", `{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.1","zone":"amer","platform":"p1","underlay_asn":65001}`)
	mustWrite("topology.json", `{"version":"1.0","nodes":{"leaf1":{}},"links":[]}`)
	n, err := NewNetwork(dir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	// A node override with no network base must succeed (no floor).
	if err := n.SetSSHCredentials(spec.ScopeNode, "leaf1", "leafuser", "leafpass"); err != nil {
		t.Fatalf("set node with no network base = %v; want nil (ssh has no network-floor invariant)", err)
	}
	if eff, err := n.resolveNodeSpecByName("leaf1"); err != nil {
		t.Fatalf("resolve: %v", err)
	} else if eff.SSHUser != "leafuser" {
		t.Errorf("effective = %q, want leafuser", eff.SSHUser)
	}
}

// TestSSHCredentials_UnknownScopeAndZone pins the not-found paths: an unknown
// zone instance and an unknown scope token both error rather than silently
// writing (fail-closed), symmetric with the map scope surface.
func TestSSHCredentials_UnknownScopeAndZone(t *testing.T) {
	n := newSSHFixture(t)
	if err := n.SetSSHCredentials(spec.ScopeZone, "nope", "u", "p"); err == nil {
		t.Error("set on unknown zone = nil; want a not-found error")
	}
	if err := n.SetSSHCredentials("galaxy", "", "u", "p"); err == nil {
		t.Error("set on unknown scope = nil; want a not-found error")
	}
	if _, err := n.GetSSHCredentialsAt(spec.ScopeZone, "nope"); err == nil {
		t.Error("read on unknown zone = nil; want a not-found error")
	}
}
