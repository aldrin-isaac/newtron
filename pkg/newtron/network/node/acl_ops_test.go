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
