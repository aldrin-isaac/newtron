package node

import (
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
	n.bindMemberQoS(cs, 400)
	assertChange(t, cs, "PORT_QOS_MAP", "Ethernet0", ChangeAdd)
	assertChange(t, cs, "PORT_QOS_MAP", "Ethernet4", ChangeAdd)
	assertNoChange(t, cs, "PORT_QOS_MAP", "Vlan400")
}

// The trunk-member QoS conflict is no longer a bindMemberQoS concern: a QoS-bearing
// irb service is refused on a VLAN with any trunk (multi-VLAN) member at apply/join
// time, so a member reaching bindMemberQoS is always single-VLAN. That gate is
// covered by TestMemberPolicy_TrunkGate (service_bridgedomain_test.go).
