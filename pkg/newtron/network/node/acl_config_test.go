package node

import (
	"context"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestMemberPolicy_TaggedAndUntagged pins that an irb-service ACL binds to BOTH
// an untagged (access) member and a single-VLAN tagged member of the same VLAN,
// with unqualified rules (each member carries only this VLAN, so per-port == per-
// VLAN, §7). Membership kind (tagged vs untagged) must not change which members
// the policy reaches, as long as each stays single-VLAN.
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
	// The ACL binds to both members — each is a single-VLAN port (Ethernet0
	// untagged, Ethernet4 tagged in VLAN 100 only), so both are policy-eligible.
	if got := n.configDB.ACLTable[acl].Ports; got != "Ethernet0,Ethernet4" {
		t.Fatalf("ACL ports = %q, want both single-VLAN members (Ethernet0, Ethernet4)", got)
	}
	// The rule is unqualified L3 — the ACLRuleEntry no longer carries a VLAN_ID
	// (per-port == per-VLAN on a single-VLAN member).
	sawRule := false
	for key := range n.configDB.ACLRule {
		if len(key) > len(acl) && key[:len(acl)+1] == acl+"|" {
			sawRule = true
		}
	}
	if !sawRule {
		t.Fatal("no ACL rule rendered")
	}
}

// TestFilterHashIsUnqualified pins §7: an irb service's ACL rules are unqualified,
// so a filter is content-addressed by its rules alone — no VLAN folds in, and two
// services sharing a filter converge on one ACL table by design. The rule fields
// carry no VLAN_ID.
func TestFilterHashIsUnqualified(t *testing.T) {
	filter := &spec.FilterSpec{Type: "ipv4", Rules: []*spec.FilterRule{{Sequence: 10, Action: "permit"}}}

	if f := buildAclRuleFields(filter.Rules[0], "", ""); f["VLAN_ID"] != "" {
		t.Fatalf("rule carries VLAN_ID = %q, want none (rules are unqualified)", f["VLAN_ID"])
	}
	// The hash is over content alone and deterministic.
	if computeFilterHash(filter) != computeFilterHash(filter) {
		t.Fatal("hash is not deterministic for a fixed filter")
	}
}
