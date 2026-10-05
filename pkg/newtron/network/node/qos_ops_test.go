package node

import (
	"fmt"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// member and irbQoSBinding build raw intents so the QoS render can be exercised
// without driving a full apply (which would validate the device-wide SCHEDULER
// rows — covered elsewhere). They mirror what configure-interface / apply-service
// write.
func trunkMember(n *Node, port string, vlanID string) {
	n.configDB.NewtronIntent["interface|"+port+"|vlan|"+vlanID] = map[string]string{
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

// TestUnbindQoS_ClearsShrunkPolicyExtent is the regression this owns: a policy
// shrinks while bound, and the teardown must still clear what the bind delivered.
//
// The shrink is a first-class operator action (`newtron qos remove-queue`, which
// carries no binding guard), and the value teardown needs is not knowable when it
// runs — the projection and the intent record are both re-derived through current
// specs, so each reports two queues where the device holds four. Clearing the
// namespace the port owns is what makes the reverse independent of that.
//
// Before the fix the teardown counted the shrunken spec and deleted queues 0-1,
// leaving 2-3 on the device with no operation able to remove them.
func TestUnbindQoS_ClearsShrunkPolicyExtent(t *testing.T) {
	n := testDevice()
	sp := n.SpecProvider.(*testSpecProvider)
	fourQueues := &spec.QoSPolicy{Queues: []*spec.QoSQueue{
		{Type: "strict"}, {Type: "strict"}, {Type: "dwrr", Weight: 50, ECN: true}, {Type: "dwrr", Weight: 50},
	}}
	sp.qosPolicies["GOLD"] = fourQueues

	// BindQoS creates the interface identity intent itself.
	i := n.interfaces["Ethernet0"]
	if _, err := i.BindQoS(t.Context(), "GOLD"); err != nil {
		t.Fatalf("BindQoS: %v", err)
	}
	for idx := 0; idx < 4; idx++ {
		if _, ok := n.configDB.Queue[fmt.Sprintf("Ethernet0|%d", idx)]; !ok {
			t.Fatalf("bind should have delivered QUEUE|Ethernet0|%d", idx)
		}
	}

	// The operator edits the policy down to two queues; nothing on the device moved.
	sp.qosPolicies["GOLD"] = &spec.QoSPolicy{Queues: []*spec.QoSQueue{{Type: "strict"}, {Type: "strict"}}}

	cs, err := i.UnbindQoS(t.Context())
	if err != nil {
		t.Fatalf("UnbindQoS: %v", err)
	}
	for idx := 0; idx < sonic.MaxQueuesPerPort; idx++ {
		assertChange(t, cs, "QUEUE", fmt.Sprintf("Ethernet0|%d", idx), ChangeDelete)
	}
	assertChange(t, cs, "PORT_QOS_MAP", "Ethernet0", ChangeDelete)
	// Last consumer, so the policy's own namespace goes too — including the
	// scheduler for a queue the shrunken spec no longer declares, and the WRED
	// profile whose ECN queue it dropped.
	assertChange(t, cs, "SCHEDULER", "GOLD_Q3", ChangeDelete)
	assertChange(t, cs, "WRED_PROFILE", "GOLD_ECN", ChangeDelete)
	assertChange(t, cs, "DSCP_TO_TC_MAP", "GOLD", ChangeDelete)
	assertChange(t, cs, "TC_TO_QUEUE_MAP", "GOLD", ChangeDelete)
}

// TestUnbindMemberQoS_Gates pins the two conditions that replaced the projection
// row-count the per-member teardown used to do. Both read intent records.
//
// A LAG is the load-bearing case: it can be a VLAN member but holds no
// PORT_QOS_MAP row (the schema refuses the key), so an ungated sweep would fail
// every UnconfigureInterface on a PortChannel — which the old row-count prevented
// only as a side effect of always counting zero there.
func TestUnbindMemberQoS_Gates(t *testing.T) {
	n := testDevice()
	n.configDB.Port["PortChannel1"] = sonic.PortEntry{}
	n.interfaces["PortChannel1"] = &Interface{node: n, name: "PortChannel1"}
	n.SpecProvider.(*testSpecProvider).qosPolicies["QOS1"] = &spec.QoSPolicy{}

	t.Run("LAG member emits no PORT_QOS_MAP", func(t *testing.T) {
		trunkMember(n, "PortChannel1", "400")
		irbQoSBinding(n, "400", "QOS1", "svc-a")
		delete(n.configDB.NewtronIntent, "interface|PortChannel1|vlan|400")

		cs := NewChangeSet(n.Name(), "test")
		n.unbindMemberQoS(cs, "PortChannel1", map[int]bool{400: true})
		assertNoChange(t, cs, "PORT_QOS_MAP", "PortChannel1")
		assertNoChange(t, cs, "QUEUE", "PortChannel1|0")
	})

	t.Run("VLAN without QoS emits nothing", func(t *testing.T) {
		n2 := testDevice()
		n2.configDB.NewtronIntent[bindingKey("Vlan500")] = map[string]string{
			"operation": sonic.OpApplyService, "state": "actuated",
			sonic.FieldVLANID: "500", sonic.FieldServiceName: "plain",
		}
		cs := NewChangeSet(n2.Name(), "test")
		n2.unbindMemberQoS(cs, "Ethernet0", map[int]bool{500: true})
		assertNoChange(t, cs, "PORT_QOS_MAP", "Ethernet0")
	})

	t.Run("departed QoS VLAN clears the port", func(t *testing.T) {
		n3 := testDevice()
		n3.SpecProvider.(*testSpecProvider).qosPolicies["QOS1"] = &spec.QoSPolicy{}
		irbQoSBinding(n3, "400", "QOS1", "svc-a")
		cs := NewChangeSet(n3.Name(), "test")
		n3.unbindMemberQoS(cs, "Ethernet0", map[int]bool{400: true})
		assertChange(t, cs, "PORT_QOS_MAP", "Ethernet0", ChangeDelete)
		assertChange(t, cs, "QUEUE", "Ethernet0|0", ChangeDelete)
	})
}
