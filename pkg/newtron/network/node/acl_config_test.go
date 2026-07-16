package node

import (
	"context"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestMemberPolicy_TaggedAndUntagged pins the §38 VLAN_ID battery at the config
// shape: an irb-service ACL binds to BOTH an untagged (access) member and a
// tagged (trunk) member of the same VLAN, and its rule is VLAN-qualified — the
// outer-VLAN match works the same whichever way the member joined (a trunk
// member carries the tag; an untagged member's traffic is classified to the PVID,
// §7). Membership kind must not change which members the policy reaches.
func TestMemberPolicy_TaggedAndUntagged(t *testing.T) {
	ctx := context.Background()
	n, e0 := testInterface() // Ethernet0 — will join untagged (access)
	e4, err := n.GetInterface("Ethernet4") // Ethernet4 — will join tagged (trunk)
	if err != nil {
		t.Fatalf("GetInterface Ethernet4: %v", err)
	}
	n.SpecProvider.(*testSpecProvider).services["CUST_IRB_ACL"] =
		&spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB, IngressFilter: "FILTER1"}
	n.SpecProvider.(*testSpecProvider).filterSpecs["FILTER1"] =
		&spec.FilterSpec{Type: "ipv4", Rules: []*spec.FilterRule{{Sequence: 10, Action: "permit"}}}

	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := e0.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("ConfigureInterface(Ethernet0 untagged): %v", err)
	}
	if _, err := e4.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
		t.Fatalf("ConfigureInterface(Ethernet4 tagged): %v", err)
	}
	if _, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("ConfigureIRB: %v", err)
	}
	irb, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface(Vlan100): %v", err)
	}
	if _, err := irb.ApplyService(ctx, "CUST_IRB_ACL", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}

	acl := n.GetIntent(bindingKey("Vlan100")).Params["ingress_acl"]
	// The ACL binds to the untagged AND the tagged member.
	if got := n.configDB.ACLTable[acl].Ports; got != "Ethernet0,Ethernet4" {
		t.Fatalf("ACL ports = %q, want both the untagged (Ethernet0) and tagged (Ethernet4) member", got)
	}
	// The rule is VLAN-qualified.
	sawRule := false
	for key, r := range n.configDB.ACLRule {
		if len(key) > len(acl) && key[:len(acl)+1] == acl+"|" {
			if r.VLANID != "100" {
				t.Fatalf("rule %s VLAN_ID = %q, want 100", key, r.VLANID)
			}
			sawRule = true
		}
	}
	if !sawRule {
		t.Fatal("no VLAN-qualified rule rendered")
	}
}

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
