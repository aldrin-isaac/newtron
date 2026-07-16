package node

import (
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestVLANQualifiedRules pins the D-3 half of per-member policy (§7): an irb-type
// service's ACL is VLAN-qualified. The VLAN folds into both the content hash (so
// a filter shared by two services on different VLANs yields distinct ACL names —
// no rule-key collision when a trunk member belongs to both) and each rule (the
// VLAN_ID match, so the rule matches only its VLAN's traffic). vlanID 0, a
// per-port service, adds no qualifier.
func TestVLANQualifiedRules(t *testing.T) {
	filter := &spec.FilterSpec{Type: "ipv4", Rules: []*spec.FilterRule{{Sequence: 10, Action: "permit"}}}

	// The VLAN_ID match lands on the rule only when vlanID > 0.
	if f := buildAclRuleFields(filter.Rules[0], "", "", 400); f["VLAN_ID"] != "400" {
		t.Fatalf("vlan 400: VLAN_ID = %q, want 400", f["VLAN_ID"])
	}
	if f := buildAclRuleFields(filter.Rules[0], "", "", 0); f["VLAN_ID"] != "" {
		t.Fatalf("per-port (vlan 0): VLAN_ID = %q, want empty", f["VLAN_ID"])
	}

	// Different VLANs → different content hashes → different ACL names, so two
	// services sharing this filter do not collide on one table.
	h400 := computeFilterHash(filter, 400)
	h500 := computeFilterHash(filter, 500)
	h0 := computeFilterHash(filter, 0)
	if h400 == h500 {
		t.Fatalf("vlan 400 and 500 hashed the same (%s) — the two ACLs would collide", h400)
	}
	if h400 == h0 || h500 == h0 {
		t.Fatal("a vlan-qualified hash matched the unqualified hash — the VLAN did not fold in")
	}
	// Same VLAN is stable (content-addressed).
	if computeFilterHash(filter, 400) != h400 {
		t.Fatal("hash is not deterministic for a fixed (filter, vlan)")
	}
}
