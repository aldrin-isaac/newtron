package node

import (
	"sort"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// TestACLPortsFromIntents_ServiceBindingKey pins the Phase C straggler fix:
// service bindings re-keyed to interface|<name>|service, and the ports-list
// scan must extract the bare interface name (Ethernet0), not the sub-resource
// suffix (Ethernet0|service). A corrupted ports entry would be delivered to
// the ACL_TABLE ports field. Two interfaces bind the same service filter.
func TestACLPortsFromIntents_ServiceBindingKey(t *testing.T) {
	n := testDevice()
	for _, port := range []string{"Ethernet0", "Ethernet4"} {
		n.configDB.NewtronIntent[bindingKey(port)] = map[string]string{
			"operation":   sonic.OpApplyService,
			"state":       "actuated",
			"ingress_acl": "EDGE_IN",
			"_parents":    "interface|" + port,
		}
	}

	ports := n.aclPortsFromIntents("EDGE_IN", "ingress")
	got := strings.Split(ports, ",")
	sort.Strings(got)
	want := []string{"Ethernet0", "Ethernet4"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("aclPortsFromIntents = %q, want the bare port names %v", ports, want)
	}
}

// TestACLPortsFromIntents_IRBMemberBindings pins per-member ACL policy
// (irb-service-redesign.md §4): an irb-type service binds to the IRB (VlanN), but
// a VLAN interface is not an ACL bind point — the policy is realized on the VLAN's
// member ports. aclPortsFromIntents must expand the IRB binding to the members
// that exist in the intent DB, and do so order-independently (the set is the same
// whether the members or the binding was authored first).
func TestACLPortsFromIntents_IRBMemberBindings(t *testing.T) {
	member := func(n *Node, port string) {
		n.configDB.NewtronIntent["interface|"+port] = map[string]string{
			"operation":       sonic.OpConfigureInterface,
			"state":           "actuated",
			sonic.FieldVLANID: "100",
		}
	}
	binding := func(n *Node) {
		n.configDB.NewtronIntent[bindingKey("Vlan100")] = map[string]string{
			"operation":       sonic.OpApplyService,
			"state":           "actuated",
			"ingress_acl":     "EDGE_IN",
			sonic.FieldVLANID: "100",
			"_parents":        "interface|Vlan100",
		}
	}

	t.Run("binding but no members → no ports (no member bindings)", func(t *testing.T) {
		n := testDevice()
		binding(n)
		if got := n.aclPortsFromIntents("EDGE_IN", "ingress"); got != "" {
			t.Fatalf("no members → no ports, got %q", got)
		}
	})

	t.Run("members before binding", func(t *testing.T) {
		n := testDevice()
		member(n, "Ethernet0")
		member(n, "Ethernet4")
		binding(n)
		if got := n.aclPortsFromIntents("EDGE_IN", "ingress"); got != "Ethernet0,Ethernet4" {
			t.Fatalf("want members (not the IRB), got %q", got)
		}
	})

	t.Run("binding before members — same result (order-independent)", func(t *testing.T) {
		n := testDevice()
		binding(n)
		member(n, "Ethernet0")
		member(n, "Ethernet4")
		if got := n.aclPortsFromIntents("EDGE_IN", "ingress"); got != "Ethernet0,Ethernet4" {
			t.Fatalf("member-after-binding must be picked up, got %q", got)
		}
	})
}

// TestResourceInterfaceName pins the shared name-extraction helper across
// identity records and every sub-resource form.
func TestResourceInterfaceName(t *testing.T) {
	tests := []struct{ resource, want string }{
		{"interface|Ethernet0", "Ethernet0"},
		{"interface|Ethernet0|service", "Ethernet0"},
		{"interface|Ethernet0|acl|ingress", "Ethernet0"},
		{"interface|Ethernet0|trunk-vlan|100", "Ethernet0"},
		{"interface|Vlan100|service", "Vlan100"},
		{"vlan|100", ""},
		{"device", ""},
	}
	for _, tt := range tests {
		if got := resourceInterfaceName(tt.resource); got != tt.want {
			t.Errorf("resourceInterfaceName(%q) = %q, want %q", tt.resource, got, tt.want)
		}
	}
}
