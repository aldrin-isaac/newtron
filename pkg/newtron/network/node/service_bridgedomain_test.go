package node

import (
	"context"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestRefreshService_IRBKeepsGateway pins #624: refresh re-delivers the service
// without flapping the composed gateway. RemoveService-via-refresh (soft remove)
// leaves the SVI standing, so the idempotent re-apply reuses it — the refresh
// succeeds (no "needs gateway address"), and the SVI/VLAN_INTERFACE survives.
func TestRefreshService_IRBKeepsGateway(t *testing.T) {
	ctx := context.Background()
	n, e0 := testInterface()
	n.SpecProvider.(*testSpecProvider).services["LOCAL_IRB"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := e0.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("member join: %v", err)
	}
	irb, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := irb.ApplyService(ctx, "LOCAL_IRB", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}
	if _, ok := n.configDB.VLANInterface["Vlan100"]; !ok {
		t.Fatal("apply must compose the SVI")
	}

	if _, err := irb.RefreshService(ctx); err != nil {
		t.Fatalf("refresh must succeed and preserve the gateway (#624): %v", err)
	}
	if it := n.GetIntent("interface|Vlan100"); it == nil || it.Operation != sonic.OpConfigureIRB {
		t.Fatalf("the SVI must survive refresh (kept, not reaped+recreated), got %+v", it)
	}
	if _, ok := n.configDB.VLANInterface["Vlan100"]; !ok {
		t.Fatal("SVI VLAN_INTERFACE must survive refresh")
	}
	if n.GetIntent(bindingKey("Vlan100")) == nil {
		t.Fatal("the service binding must be re-delivered by refresh")
	}
}

// TestComposite_LocalIRB_MacvpnNoL2VNI pins the vni=0 macvpn case (found cold in
// the 2node-vs-service provision): a local irb references a macvpn purely for its
// vlan_id (VNI 0, no overlay). The composite must NOT bind an L2VNI — BindMACVPN
// with VNI 0 writes a schema-refused VXLAN_TUNNEL_MAP and needs a VTEP the local
// service does not have.
func TestComposite_LocalIRB_MacvpnNoL2VNI(t *testing.T) {
	ctx := context.Background()
	n, e0 := testInterface()
	sp := n.SpecProvider.(*testSpecProvider)
	sp.macvpn["LOCAL100"] = &spec.MACVPNSpec{VlanID: 100, VNI: 0}
	sp.services["LOCAL_IRB"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB, MACVPN: "LOCAL100"}
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := e0.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("member join: %v", err)
	}
	irb, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := irb.ApplyService(ctx, "LOCAL_IRB", ApplyServiceOpts{IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("local irb with a vni=0 macvpn must compose without an L2VNI: %v", err)
	}
	if _, ok := n.configDB.VLANInterface["Vlan100"]; !ok {
		t.Fatal("the SVI must still be composed")
	}
	if n.GetIntent("macvpn|100") != nil {
		t.Fatal("a local irb (vni=0 macvpn) must NOT bind an L2VNI — no macvpn intent")
	}
}

// TestComposite_EVPNIRB_DAGAirtight proves the intent DAG the evpn-irb composite
// builds is airtight: every composed intent has the correct parents, the binding
// is a child of the SVI (so unconfigure-irb is I5-guarded), remove reaps the L3 it
// composed (SVI + shared VRF/ipvpn on the last consumer) and leaves the bridge
// domain (VLAN/macvpn), no intent is left orphaned (every parent still exists), and
// the projection reconstructs identically.
func TestComposite_EVPNIRB_DAGAirtight(t *testing.T) {
	ctx := context.Background()
	n, _ := testInterface()
	sp := n.SpecProvider.(*testSpecProvider)
	sp.macvpn["SVC_VLAN400"] = &spec.MACVPNSpec{VlanID: 400, VNI: 10400, AnycastIP: "10.4.0.1/24", AnycastMAC: "00:00:00:01:04:00", ARPSuppression: true}
	sp.ipvpn["IRB"] = &spec.IPVPNSpec{L3VNI: 50400, RouteTargets: []string{"65000:50400"}}
	// A QoS policy too, so the SVI gets a second child (interface|Vlan400|qos)
	// besides the binding — proving the reap fires only once ALL children are gone.
	sp.qosPolicies["Q1"] = &spec.QoSPolicy{}
	sp.services["EVPN_IRB"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeEVPNIRB, VRFType: spec.VRFTypeShared, IPVPN: "IRB", MACVPN: "SVC_VLAN400", QoSPolicy: "Q1"}

	// EVPN preconditions need a VTEP the projection carries (HasVTEP). This test
	// has no resolved loopback and seeds after the initial setup, so give it both
	// halves of "device set up with an explicit VTEP source": the VXLAN_TUNNEL in
	// the current projection (for the ApplyService below) AND source_ip on the
	// device intent, so the setup-device replay rebuilds that VXLAN_TUNNEL on the
	// reconstruction this test performs later. BGP is present (device intent exists).
	n.configDB.VXLANTunnel["vtep1"] = sonic.VXLANTunnelEntry{SrcIP: "10.255.0.1"}
	if dev, ok := n.configDB.NewtronIntent["device"]; ok {
		dev["source_ip"] = "10.255.0.1"
	}
	if _, err := n.CreateVLAN(ctx, 400, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	irb, err := n.GetInterface("Vlan400")
	if err != nil {
		t.Fatalf("GetInterface(Vlan400): %v", err)
	}
	// No gateway opts — the anycast IP/MAC come from the SVC_VLAN400 macvpn (§7).
	if _, err := irb.ApplyService(ctx, "EVPN_IRB", ApplyServiceOpts{}); err != nil {
		t.Fatalf("ApplyService(evpn-irb composite): %v", err)
	}

	// hasParent asserts an intent exists and lists the wanted parent.
	hasParent := func(res, wantParent string) {
		t.Helper()
		it := n.GetIntent(res)
		if it == nil {
			t.Fatalf("intent %q was not composed", res)
		}
		for _, p := range it.Parents {
			if p == wantParent {
				return
			}
		}
		t.Fatalf("intent %q parents = %v, want to include %q", res, it.Parents, wantParent)
	}
	// The composed edges (§5).
	hasParent("macvpn|400", "vlan|400")            // L2VNI under the VLAN
	hasParent("vrf|Vrf_IRB", "device")             // VRF at the root
	hasParent("ipvpn|IRB", "vrf|Vrf_IRB")          // L3VNI under the VRF
	hasParent("interface|Vlan400", "vlan|400")     // SVI under the VLAN
	hasParent("interface|Vlan400", "vrf|Vrf_IRB")  // SVI bound into the VRF
	hasParent("interface|Vlan400|service", "interface|Vlan400") // binding is the SVI's child
	hasParent("interface|Vlan400|qos", "interface|Vlan400")     // qos is a second SVI child
	// The SVI records the binding as a child — this is what makes unconfigure-irb
	// refuse (I5) while the service is bound.
	svi := n.GetIntent("interface|Vlan400")
	foundChild := false
	for _, c := range svi.Children {
		if c == "interface|Vlan400|service" {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatalf("SVI children = %v, want the binding as a child (I5 guard)", svi.Children)
	}

	// Remove and prove the teardown is complete and orphan-free.
	if _, err := irb.RemoveService(ctx); err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	gone := []string{"interface|Vlan400|service", "interface|Vlan400|qos", "interface|Vlan400", "vrf|Vrf_IRB", "ipvpn|IRB"}
	for _, res := range gone {
		if n.GetIntent(res) != nil {
			t.Fatalf("intent %q must be reaped on remove (L3 the composite delivered / last consumer)", res)
		}
	}
	stays := []string{"vlan|400", "macvpn|400"} // the L2 bridge domain outlives the L3 service
	for _, res := range stays {
		if n.GetIntent(res) == nil {
			t.Fatalf("intent %q must survive remove (bridge domain, not the service's to reap)", res)
		}
	}
	// No orphan: every surviving intent's parents still exist.
	for res := range n.configDB.NewtronIntent {
		it := n.GetIntent(res)
		if it == nil {
			continue
		}
		for _, p := range it.Parents {
			if p == "device" {
				continue
			}
			if n.GetIntent(p) == nil {
				t.Fatalf("orphan: intent %q references missing parent %q", res, p)
			}
		}
	}

	// Reconstruction is identical: the reaped SVI is absent from the rebuild.
	intents := map[string]map[string]string{}
	for k, v := range n.configDB.NewtronIntent {
		cp := map[string]string{}
		for kk, vv := range v {
			cp[kk] = vv
		}
		intents[k] = cp
	}
	if err := n.RebuildProjectionFromIntents(ctx, intents); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if _, ok := n.configDB.VLANInterface["Vlan400"]; ok {
		t.Fatal("SVI must be absent from the rebuilt projection after remove")
	}
}

// TestMemberPolicy_TrunkGate pins the §7 fail-closed gate: an irb service's
// filter/QoS is delivered to member ports (SONiC cannot bind it to the IRB), which
// is correct only on single-VLAN members. So a policy-bearing service is refused on
// a VLAN with any trunk member — from both directions (apply and membership join),
// and when a filter is added to a plain service and re-delivered via refresh. A
// plain service (no filter/QoS) is unaffected: a trunk member is fine.
func TestMemberPolicy_TrunkGate(t *testing.T) {
	ctx := context.Background()
	newFilter := func() *spec.FilterSpec {
		return &spec.FilterSpec{Type: "ipv4", Rules: []*spec.FilterRule{{Sequence: 10, Action: "permit"}}}
	}
	// setup builds a VLAN 100 with e0 (access) and e4 (a trunk: also in VLAN 200),
	// an authored IRB, and returns the Vlan100 interface.
	setup := func(t *testing.T, svc *spec.ServiceSpec) (*Node, *Interface) {
		n, e0 := testInterface()
		e4, err := n.GetInterface("Ethernet4")
		if err != nil {
			t.Fatalf("GetInterface(Ethernet4): %v", err)
		}
		n.SpecProvider.(*testSpecProvider).services["SVC"] = svc
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN 100: %v", err)
		}
		if _, err := n.CreateVLAN(ctx, 200, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN 200: %v", err)
		}
		if _, err := e0.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
			t.Fatalf("e0 join 100: %v", err)
		}
		if _, err := e4.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
			t.Fatalf("e4 join 100: %v", err)
		}
		if _, err := e4.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200, Tagged: true}); err != nil {
			t.Fatalf("e4 join 200 (becomes trunk): %v", err)
		}
		if _, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"}); err != nil {
			t.Fatalf("ConfigureIRB: %v", err)
		}
		irb, err := n.GetInterface("Vlan100")
		if err != nil {
			t.Fatalf("GetInterface(Vlan100): %v", err)
		}
		return n, irb
	}

	t.Run("plain service allowed with a trunk member", func(t *testing.T) {
		_, irb := setup(t, &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB})
		if _, err := irb.ApplyService(ctx, "SVC", ApplyServiceOpts{VLAN: 100}); err != nil {
			t.Fatalf("a plain irb service (no filter/QoS) must be allowed on a trunk VLAN: %v", err)
		}
	})

	t.Run("apply refused when the service carries a filter", func(t *testing.T) {
		n, irb := setup(t, &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB, IngressFilter: "F1"})
		n.SpecProvider.(*testSpecProvider).filterSpecs["F1"] = newFilter()
		_, err := irb.ApplyService(ctx, "SVC", ApplyServiceOpts{VLAN: 100})
		if err == nil {
			t.Fatal("apply of a filter-bearing irb service must be refused with a trunk member")
		}
		if !strings.Contains(err.Error(), "Ethernet4") {
			t.Fatalf("gate error must name the trunk member Ethernet4: %v", err)
		}
	})

	t.Run("filter added later is gated at refresh", func(t *testing.T) {
		svc := &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB} // starts plain
		n, irb := setup(t, svc)
		if _, err := irb.ApplyService(ctx, "SVC", ApplyServiceOpts{VLAN: 100}); err != nil {
			t.Fatalf("plain apply should succeed: %v", err)
		}
		// The operator adds a filter to the service definition, then re-delivers.
		// Refresh keeps the SVI (#624), so the re-apply reaches the trunk gate.
		svc.IngressFilter = "F1"
		n.SpecProvider.(*testSpecProvider).filterSpecs["F1"] = newFilter()
		_, err := irb.RefreshService(ctx)
		if err == nil {
			t.Fatal("refresh after adding a filter must be gated by the trunk member")
		}
		if !strings.Contains(err.Error(), "Ethernet4") {
			t.Fatalf("refresh gate error must name the trunk member: %v", err)
		}
	})

	t.Run("membership join refused on a policy-serviced VLAN", func(t *testing.T) {
		// A clean VLAN 100 with only an access member, a filter-bearing service.
		n, e0 := testInterface()
		n.SpecProvider.(*testSpecProvider).services["SVC"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB, IngressFilter: "F1"}
		n.SpecProvider.(*testSpecProvider).filterSpecs["F1"] = newFilter()
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN 100: %v", err)
		}
		if _, err := n.CreateVLAN(ctx, 200, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN 200: %v", err)
		}
		if _, err := e0.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
			t.Fatalf("e0 join 100: %v", err)
		}
		if _, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"}); err != nil {
			t.Fatalf("ConfigureIRB: %v", err)
		}
		irb, _ := n.GetInterface("Vlan100")
		if _, err := irb.ApplyService(ctx, "SVC", ApplyServiceOpts{VLAN: 100}); err != nil {
			t.Fatalf("apply on an all-access VLAN should succeed: %v", err)
		}
		// e0 now carries the service's policy; making it a trunk must be refused.
		if _, err := e0.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200, Tagged: true}); err == nil {
			t.Fatal("making a policy-member a trunk (joining VLAN 200) must be refused")
		}
	})
}

// TestApplyService_BridgedComposesBridgeDomain pins the bridged / evpn-bridged
// composite (B): the service assembles the L2 bridge domain it delivers — the
// VLAN and this port's untagged access membership (its delivery point) — reusing
// any piece the operator pre-authored (§2). The one thing it cannot derive is
// which VLAN, so it requires that. Reap flags (created_vlan/created_membership)
// record what apply created so remove reaps only that (§15, reference-aware).
func TestApplyService_BridgedComposesBridgeDomain(t *testing.T) {
	ctx := context.Background()

	t.Run("refused without a VLAN", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
		_, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{})
		if err == nil || !strings.Contains(err.Error(), "requires a VLAN") {
			t.Fatalf("want VLAN-required refusal, got %v", err)
		}
	})

	t.Run("assembles the VLAN and access membership when absent", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
		cs, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100})
		if err != nil {
			t.Fatalf("ApplyService (assemble): %v", err)
		}
		// The composite creates the VLAN and this port's untagged membership.
		assertChange(t, cs, "VLAN", "Vlan100", ChangeAdd)
		assertChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeAdd)
		if n.GetIntent("vlan|100") == nil {
			t.Fatalf("VLAN intent not created")
		}
		if !n.isVLANMember("Ethernet0", 100) {
			t.Fatalf("access membership intent not created")
		}
		// No reap flags: both the VLAN and the membership are reaped on the last
		// consumer (childlessness), the same rule the SVI/VRF reaps use.
	})

	t.Run("reuses an operator-authored VLAN and membership", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN: %v", err)
		}
		if _, err := intf.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
			t.Fatalf("ConfigureInterface (membership): %v", err)
		}
		svcCS, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100})
		if err != nil {
			t.Fatalf("ApplyService onto a pre-authored bridge domain: %v", err)
		}
		// Idempotent: the composite re-adds neither the VLAN nor the membership.
		assertNoChange(t, svcCS, "VLAN_MEMBER", "Vlan100|Ethernet0")
	})
}

// TestApplyService_IRBRequiresConfiguredIRB pins the delivery-point flip: an
// irb-type service binds to the VLAN's L3 gateway — the IRB — and only the IRB
// (irb-service-redesign.md §3). It refuses a physical port (the capability
// gate: a port is no bridge-domain gateway), and on the IRB it refuses until
// configure-irb has authored the gateway (§6: the service binds to an SVI it
// does not create). Where it succeeds, it writes no VLAN_INTERFACE row.
func TestApplyService_IRBRequiresConfiguredIRB(t *testing.T) {
	ctx := context.Background()

	t.Run("refused on a physical port", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
		_, err := intf.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100})
		if err == nil || !strings.Contains(err.Error(), "gateway") {
			t.Fatalf("want capability refusal (a physical port is no gateway), got %v", err)
		}
	})

	t.Run("refused on the IRB before configure-irb", func(t *testing.T) {
		n, _ := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN: %v", err)
		}
		irb, err := n.GetInterface("Vlan100")
		if err != nil {
			t.Fatalf("GetInterface(Vlan100): %v", err)
		}
		_, err = irb.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100})
		if err == nil || !strings.Contains(err.Error(), "configure-irb") {
			t.Fatalf("want configure-irb-first refusal, got %v", err)
		}
	})

	t.Run("succeeds on the IRB, does not author the SVI", func(t *testing.T) {
		n, _ := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN: %v", err)
		}
		if _, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"}); err != nil {
			t.Fatalf("ConfigureIRB: %v", err)
		}
		irb, err := n.GetInterface("Vlan100")
		if err != nil {
			t.Fatalf("GetInterface(Vlan100): %v", err)
		}
		svcCS, err := irb.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100})
		if err != nil {
			t.Fatalf("ApplyService(irb) on the IRB: %v", err)
		}
		// The service binds; it does NOT write the SVI (configure-irb's, §6).
		assertNoChange(t, svcCS, "VLAN_INTERFACE", "Vlan100")
		// The binding is a sub-resource of the IRB identity (interface|Vlan100|service).
		if n.GetIntent(bindingKey("Vlan100")) == nil {
			t.Fatal("service binding intent missing on the IRB")
		}
	})
}

// TestMemberPolicy_ACLFollowsMembers pins per-member ACL policy end to end
// policy (irb-service-redesign.md §4): a filter-bearing irb service binds to the
// IRB, and its ACL_TABLE ports follow the VLAN's members — the pre-existing member
// is swept at apply, a later joiner is added, and a leaver is dropped. The ports
// are never the IRB itself (a VLAN interface is no ACL bind point, §7).
func TestMemberPolicy_ACLFollowsMembers(t *testing.T) {
	ctx := context.Background()
	n, e0 := testInterface() // Ethernet0
	e4, err := n.GetInterface("Ethernet4")
	if err != nil {
		t.Fatalf("GetInterface Ethernet4: %v", err)
	}
	sp := n.SpecProvider.(*testSpecProvider)
	sp.services["CUST_IRB_ACL"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB, IngressFilter: "FILTER1"}
	sp.filterSpecs["FILTER1"] = &spec.FilterSpec{Type: "ipv4", Rules: []*spec.FilterRule{{Sequence: 10}}}

	// Bridge domain + gateway; Ethernet0 is a member BEFORE the service (Case 1).
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := e0.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("ConfigureInterface(Ethernet0): %v", err)
	}
	if _, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("ConfigureIRB: %v", err)
	}
	irb, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface(Vlan100): %v", err)
	}

	// Apply the filter-bearing irb service — the ACL materializes on the
	// pre-existing member (the sweep), not on the IRB.
	if _, err := irb.ApplyService(ctx, "CUST_IRB_ACL", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService(irb + filter): %v", err)
	}
	acl := n.GetIntent(bindingKey("Vlan100")).Params["ingress_acl"]
	if acl == "" {
		t.Fatal("binding did not record an ingress_acl")
	}
	ports := func() string { return n.configDB.ACLTable[acl].Ports }
	if got := ports(); got != "Ethernet0" {
		t.Fatalf("apply sweep: ACL ports = %q, want the pre-existing member Ethernet0", got)
	}
	// The rules are unqualified L3 (§7): the member is a single-VLAN (access)
	// port, so per-port == per-VLAN with no VLAN qualifier. A rule was rendered.
	sawRule := false
	for key := range n.configDB.ACLRule {
		if strings.HasPrefix(key, acl+"|") {
			sawRule = true
		}
	}
	if !sawRule {
		t.Fatal("no ACL rule was rendered")
	}

	// A member joins AFTER the service (Case 2) — the ACL follows it.
	if _, err := e4.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("ConfigureInterface(Ethernet4 join): %v", err)
	}
	if got := ports(); got != "Ethernet0,Ethernet4" {
		t.Fatalf("member join: ACL ports = %q, want Ethernet0,Ethernet4", got)
	}

	// A member leaves — its binding goes with it.
	if _, err := e0.UnconfigureInterface(ctx); err != nil {
		t.Fatalf("UnconfigureInterface(Ethernet0 leave): %v", err)
	}
	if got := ports(); got != "Ethernet4" {
		t.Fatalf("member leave: ACL ports = %q, want Ethernet4", got)
	}

	// The member-bound ACL — table AND rules — must survive a projection
	// rebuild, or the device drifts on the next refresh (found cold, §38): a
	// service ACL's rules are written inline at apply, so the create-acl replay
	// must rebuild them from the recorded filter.
	intents := map[string]map[string]string{}
	for k, v := range n.configDB.NewtronIntent {
		cp := map[string]string{}
		for kk, vv := range v {
			cp[kk] = vv
		}
		intents[k] = cp
	}
	if err := n.RebuildProjectionFromIntents(ctx, intents); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := ports(); got != "Ethernet4" {
		t.Fatalf("after rebuild: ACL ports = %q, want Ethernet4", got)
	}
	sawRuleAfter := false
	for key := range n.configDB.ACLRule {
		if strings.HasPrefix(key, acl+"|") {
			sawRuleAfter = true
		}
	}
	if !sawRuleAfter {
		t.Fatal("the member-bound ACL rule vanished on rebuild — the device would drift")
	}

	// Remove the service after a rebuild: the ACL table AND its rules must go —
	// no orphan rule left to drift (found cold, §38). removeSharedACL reads the
	// rules from the projected ACL_RULE table, so it works even though the acl
	// intent's recorded rule list did not survive the rebuild.
	if _, err := irb.RemoveService(ctx); err != nil {
		t.Fatalf("RemoveService after rebuild: %v", err)
	}
	if _, ok := n.configDB.ACLTable[acl]; ok {
		t.Fatal("ACL table survived remove-service")
	}
	for key := range n.configDB.ACLRule {
		if strings.HasPrefix(key, acl+"|") {
			t.Fatalf("orphan ACL rule %s left after remove-service — the device drifts", key)
		}
	}
}

// TestRemoveService_IRBComposesAndReapsGateway pins the composite lifecycle: an
// irb service applied to the IRB with --ip composes the SVI gateway (no prior
// configure-irb), and remove-service reaps it on the last consumer — the same
// reference rule routed uses for its VRF (§2). Reconstruction is clean either
// way: the SVI is present after apply, absent after remove.
func TestRemoveService_IRBComposesAndReapsGateway(t *testing.T) {
	ctx := context.Background()
	n, _ := testInterface()
	n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	irb, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	// The composite authors the SVI from --ip — no standalone configure-irb.
	if _, err := irb.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}
	if _, ok := n.configDB.VLANInterface["Vlan100"]; !ok {
		t.Fatal("apply-service must compose the SVI gateway (VLAN_INTERFACE|Vlan100)")
	}
	if irbIntent := n.GetIntent("interface|Vlan100"); irbIntent == nil || irbIntent.Operation != sonic.OpConfigureIRB {
		t.Fatalf("apply-service must author the IRB identity, got %+v", irbIntent)
	}

	cs, err := irb.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	// The SVI gateway is reaped — the binding was its last consumer.
	assertChange(t, cs, "VLAN_INTERFACE", "Vlan100", ChangeDelete)
	if n.GetIntent(bindingKey("Vlan100")) != nil {
		t.Fatal("service binding must be deleted by remove-service")
	}
	if irbIntent := n.GetIntent("interface|Vlan100"); irbIntent != nil {
		t.Fatalf("the composed SVI must be reaped on the last consumer, got %+v", irbIntent)
	}
	// Reconstruction is clean: the SVI is gone from the rebuilt projection.
	intents := map[string]map[string]string{}
	for k, v := range n.configDB.NewtronIntent {
		cp := map[string]string{}
		for kk, vv := range v {
			cp[kk] = vv
		}
		intents[k] = cp
	}
	if err := n.RebuildProjectionFromIntents(ctx, intents); err != nil {
		t.Fatalf("rebuild after removal: %v", err)
	}
	if _, ok := n.configDB.VLANInterface["Vlan100"]; ok {
		t.Fatal("SVI must be absent in the rebuilt projection after remove-service")
	}
}

// TestRemoveService_BridgedReapsComposedDomain pins the bridged composite
// teardown (B): a bridged service that assembled the VLAN + this port's access
// membership reaps both on the last consumer — the reference-aware mirror of the
// irb SVI reap. The membership identity and the VLAN are gone after remove.
func TestRemoveService_BridgedReapsComposedDomain(t *testing.T) {
	ctx := context.Background()
	n, intf := testInterface()
	n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
	if _, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}
	if n.GetIntent("vlan|100") == nil || !n.isVLANMember("Ethernet0", 100) {
		t.Fatal("apply must assemble the VLAN and the access membership")
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	// Reap: the membership row and the VLAN this apply created are both removed.
	assertChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeDelete)
	assertChange(t, cs, "VLAN", "Vlan100", ChangeDelete)
	if n.GetIntent(bindingKey("Ethernet0")) != nil {
		t.Fatal("service binding must be deleted")
	}
	if n.GetIntent("interface|Ethernet0") != nil {
		t.Fatal("the created access-membership identity must be reaped on the last consumer")
	}
	if n.GetIntent("vlan|100") != nil {
		t.Fatal("the created VLAN must be reaped on the last consumer")
	}
}

// TestRemoveService_BridgedKeepsOperatorDomain pins reference-awareness: when the
// operator pre-authored the VLAN + membership, apply-service reuses them (no reap
// operator config overlapped by a service — is reaped along with the service (an
// operator resource is preserved only while no service overlaps it). Last-consumer,
// no provenance: the VLAN + membership go on the service's removal.
func TestRemoveService_BridgedReapsOverlappedOperatorDomain(t *testing.T) {
	ctx := context.Background()
	n, intf := testInterface()
	n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
	// Operator authors the VLAN + membership standalone...
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := intf.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("ConfigureInterface: %v", err)
	}
	// ...then a service overlaps them.
	if _, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}
	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	// Both are reaped with the service they overlapped — no operator protection.
	assertChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeDelete)
	assertChange(t, cs, "VLAN", "Vlan100", ChangeDelete)
	if n.isVLANMember("Ethernet0", 100) {
		t.Fatal("overlapped operator membership must be reaped with the service")
	}
	if n.GetIntent("vlan|100") != nil {
		t.Fatal("overlapped operator VLAN must be reaped with the service")
	}
}

// TestRemoveService_BridgedSharedVLANSurvives pins the last-consumer childlessness:
// two ports bridged onto one VLAN. Removing the first leaves the VLAN (the second
// member remains); removing the second reaps it.
func TestRemoveService_BridgedSharedVLANSurvives(t *testing.T) {
	ctx := context.Background()
	n, intf := testInterface()
	n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
	other, err := n.GetInterface("Ethernet4")
	if err != nil {
		t.Fatalf("GetInterface Ethernet4: %v", err)
	}
	if _, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService Ethernet0: %v", err)
	}
	if _, err := other.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService Ethernet4: %v", err)
	}
	if _, err := intf.RemoveService(ctx); err != nil {
		t.Fatalf("RemoveService Ethernet0: %v", err)
	}
	if n.GetIntent("vlan|100") == nil {
		t.Fatal("VLAN must survive while another bridged member remains")
	}
	if !n.isVLANMember("Ethernet4", 100) {
		t.Fatal("the second member must remain")
	}
	if _, err := other.RemoveService(ctx); err != nil {
		t.Fatalf("RemoveService Ethernet4: %v", err)
	}
	if n.GetIntent("vlan|100") != nil {
		t.Fatal("VLAN must be reaped once the last bridged member leaves")
	}
}

// TestApplyService_IRBReconstructs pins that the flipped irb binding round-trips
// through reconstruction. The IRB identity IS a DAG parent of the binding, so
// replay applies it first — but the precondition is gated on !reconstructing
// regardless (§20: the whole intent DB is replayed jointly, never re-validated
// against a half-rebuilt projection).
func TestApplyService_IRBReconstructs(t *testing.T) {
	ctx := context.Background()
	n, _ := testInterface()
	// Normalized key: production normalizes spec names at load, so replay's
	// NormalizeName-resolved GetService matches. The raw fixture key must too.
	n.SpecProvider.(*testSpecProvider).services["CUST_IRB"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("ConfigureIRB: %v", err)
	}
	irb, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := irb.ApplyService(ctx, "CUST_IRB", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}

	intents := map[string]map[string]string{}
	for k, v := range n.configDB.NewtronIntent {
		cp := map[string]string{}
		for kk, vv := range v {
			cp[kk] = vv
		}
		intents[k] = cp
	}
	if err := n.RebuildProjectionFromIntents(ctx, intents); err != nil {
		t.Fatalf("reconstruction must not fire the interactive precondition: %v", err)
	}
	if _, ok := n.configDB.VLANInterface["Vlan100"]; !ok {
		t.Fatal("SVI missing after reconstruction")
	}
	if n.GetIntent(bindingKey("Vlan100")) == nil {
		t.Fatal("binding missing after reconstruction")
	}
}

// TestApplyService_BridgedReconstructs pins that the bridged composite round-trips
// through reconstruction: replaying the binding re-assembles the VLAN and this
// port's access membership (both are DAG ancestors of the binding, applied first).
// The trunk-eligibility gate and membership write are skipped/idempotent under
// !reconstructing, so the rebuilt projection matches (§20).
func TestApplyService_BridgedReconstructs(t *testing.T) {
	ctx := context.Background()
	n, intf := testInterface()
	n.SpecProvider.(*testSpecProvider).services["CUST_BRIDGED"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
	if _, err := intf.ApplyService(ctx, "CUST_BRIDGED", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}

	intents := map[string]map[string]string{}
	for k, v := range n.configDB.NewtronIntent {
		cp := map[string]string{}
		for kk, vv := range v {
			cp[kk] = vv
		}
		intents[k] = cp
	}
	if err := n.RebuildProjectionFromIntents(ctx, intents); err != nil {
		t.Fatalf("reconstruction must not fire the interactive precondition: %v", err)
	}
	if n.GetIntent("vlan|100") == nil {
		t.Fatal("composed VLAN missing after reconstruction")
	}
	if !n.isVLANMember("Ethernet0", 100) {
		t.Fatal("composed access membership missing after reconstruction")
	}
	if n.GetIntent(bindingKey("Ethernet0")) == nil {
		t.Fatal("binding missing after reconstruction")
	}
}
