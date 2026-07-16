package node

import (
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// member and irbQoSBinding build raw intents so the QoS render can be exercised
// without driving a full apply (which would validate the device-wide SCHEDULER
// rows — covered elsewhere). They mirror what configure-interface / apply-service
// write.
func trunkMember(n *Node, port string, vlanID string) {
	n.configDB.NewtronIntent["interface|"+port+"|trunk-vlan|"+vlanID] = map[string]string{
		"operation":       sonic.OpAddTrunkVLAN,
		"state":           "actuated",
		sonic.FieldVLANID: vlanID,
	}
}

func irbQoSBinding(n *Node, vlan, policy, service string) {
	n.configDB.NewtronIntent[bindingKey("Vlan"+vlan)] = map[string]string{
		"operation":            sonic.OpApplyService,
		"state":                "actuated",
		sonic.FieldVLANID:      vlan,
		"qos_policy":           policy,
		sonic.FieldServiceName: service,
		"_parents":             "interface|Vlan" + vlan,
	}
}

// TestMemberPolicy_QoSBindsMembers pins the QoS half of per-member policy
// (§4): an irb-service's QoS lands on the VLAN's member ports (PORT_QOS_MAP per
// member), never on the IRB (a VLAN interface is no QoS bind point, §7).
func TestMemberPolicy_QoSBindsMembers(t *testing.T) {
	n := testDevice()
	n.SpecProvider.(*testSpecProvider).qosPolicies["QOS1"] = &spec.QoSPolicy{}
	trunkMember(n, "Ethernet0", "400")
	trunkMember(n, "Ethernet4", "400")
	irbQoSBinding(n, "400", "QOS1", "svc-a")

	cs := NewChangeSet(n.Name(), "test")
	if err := n.bindMemberQoS(cs, 400); err != nil {
		t.Fatalf("bindMemberQoS: %v", err)
	}
	assertChange(t, cs, "PORT_QOS_MAP", "Ethernet0", ChangeAdd)
	assertChange(t, cs, "PORT_QOS_MAP", "Ethernet4", ChangeAdd)
	assertNoChange(t, cs, "PORT_QOS_MAP", "Vlan400")
}

// TestMemberPolicy_QoSConflict pins the fail-closed conflict (§7): a trunk
// member on two serviced VLANs whose services carry different QoS policies is
// refused before any write, naming both services — PORT_QOS_MAP is per-port with
// no VLAN qualifier, so only one policy could ever be honored.
func TestMemberPolicy_QoSConflict(t *testing.T) {
	n := testDevice()
	sp := n.SpecProvider.(*testSpecProvider)
	sp.qosPolicies["QOS_A"] = &spec.QoSPolicy{}
	sp.qosPolicies["QOS_B"] = &spec.QoSPolicy{}
	// Ethernet0 is a trunk member of BOTH VLAN 400 and 500.
	trunkMember(n, "Ethernet0", "400")
	trunkMember(n, "Ethernet0", "500")
	irbQoSBinding(n, "400", "QOS_A", "svc-a")
	irbQoSBinding(n, "500", "QOS_B", "svc-b")

	cs := NewChangeSet(n.Name(), "test")
	err := n.bindMemberQoS(cs, 400)
	if err == nil || !strings.Contains(err.Error(), "QoS conflict") {
		t.Fatalf("shared trunk member with two QoS policies must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "svc-a") || !strings.Contains(err.Error(), "svc-b") {
		t.Fatalf("conflict must name both services, got %v", err)
	}
	// Same policy on both VLANs is NOT a conflict — the member honors one policy.
	sp.qosPolicies["QOS_B"] = sp.qosPolicies["QOS_A"]
	irbQoSBinding(n, "500", "QOS_A", "svc-b")
	if err := n.bindMemberQoS(NewChangeSet(n.Name(), "test"), 400); err != nil {
		t.Fatalf("same policy on both VLANs must not conflict, got %v", err)
	}
}
