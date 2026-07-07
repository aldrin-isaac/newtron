package node

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ============================================================================
// Operation round-trip conformance (Gap 1, PR-A)
// ============================================================================
//
// The round-trip doctrine (DESIGN_PRINCIPLES_NEWTRON §20; CLAUDE.md "Intent
// Round-Trip Completeness"): every caller param that affects CONFIG_DB must
// survive writeIntent → IntentsToSteps → ReplayStep. A dropped param produces
// a perfectly valid, silently wrong reconstruction — no validator can see it,
// because completeness is not validity.
//
// This test is the behavioral net: drive every replayable operation on an
// offline node with every caller-optional param set to a non-default, export
// the intent DB to steps, replay the steps onto a fresh identical node, and
// require both the intent DB and the projection to match exactly. Any param
// that fails the round trip surfaces as a concrete field-level diff.
//
// The invocation table below is deliberately registry-shaped: op verb +
// invoke closure. PR-B promotes it into the operation registry
// (op_registry.go) and this test then walks the registry instead.

// opInvocation is one step of the round-trip sequence.
type opInvocation struct {
	op     string
	invoke func(ctx context.Context, n *Node) error
}

// iface fetches an interface or fails the invocation with context.
func iface(n *Node, name string) (*Interface, error) {
	i, err := n.GetInterface(name)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", name, err)
	}
	return i, nil
}

// roundTripNode builds the offline node both sides of the round trip start
// from: testDevice() plus the extra ports and specs the sequence needs.
// Called once per side so A and B are structurally identical.
func roundTripNode() *Node {
	n := testDevice()
	for _, p := range []string{"Ethernet8", "Ethernet12", "Ethernet16", "Ethernet20"} {
		n.configDB.Port[p] = sonic.PortEntry{}
		n.interfaces[p] = &Interface{node: n, name: p}
	}
	sp := n.SpecProvider.(*testSpecProvider)
	sp.macvpn["SERVERS"] = &spec.MACVPNSpec{
		Description:    "server macvpn",
		VlanID:         200,
		VNI:            10200,
		RouteTargets:   []string{"65000:200"},
		ARPSuppression: true,
	}
	// IP-VPN spec names never carry the Vrf_ prefix — DeriveVRFNameForIPVPN
	// prepends it, so ipvpn "CUST" binds into VRF "Vrf_CUST".
	sp.ipvpn["CUST"] = &spec.IPVPNSpec{
		Description:  "customer ipvpn",
		L3VNI:        50400,
		L3VNIVlan:    3999,
		RouteTargets: []string{"65000:50400"},
	}
	sp.services["TRANSIT"] = &spec.ServiceSpec{
		Description: "transit underlay",
		ServiceType: "routed",
		Routing:     &spec.RoutingSpec{Protocol: "bgp", PeerAS: "request"},
	}
	sp.qosPolicies["GOLD"] = &spec.QoSPolicy{}
	return n
}

// roundTripSequence drives every replayable operation in DAG-legal order.
// Every caller-sourced optional param is set to a non-default value so the
// round trip exercises the full param surface — a param left at its zero
// value cannot reveal a dropped-field bug.
var roundTripSequence = []opInvocation{
	{"setup-device", func(ctx context.Context, n *Node) error {
		_, err := n.SetupDevice(ctx, SetupDeviceOpts{
			Fields: map[string]string{
				"hostname":                   "rt-test",
				"bgp_asn":                    "65001",
				"hwsku":                      "Force10-S6000",
				"type":                       "LeafRouter",
				"docker_routing_config_mode": "unified",
			},
			SourceIP: "10.255.0.1",
			RR: &RouteReflectorOpts{
				ClusterID: "1.1.1.1",
				LocalASN:  65001,
				RouterID:  "10.255.0.1",
				LocalAddr: "10.255.0.1",
				Clients:   []RouteReflectorPeer{{IP: "10.0.0.2", ASN: 65001}},
				Peers:     []RouteReflectorPeer{{IP: "10.0.0.3", ASN: 65001}},
			},
		})
		return err
	}},
	{"create-vrf", func(ctx context.Context, n *Node) error {
		_, err := n.CreateVRF(ctx, "Vrf_TEST", VRFConfig{})
		return err
	}},
	{"create-vlan (plain)", func(ctx context.Context, n *Node) error {
		_, err := n.CreateVLAN(ctx, 100, VLANConfig{Description: "irb vlan"})
		return err
	}},
	{"create-vlan (macvpn target)", func(ctx context.Context, n *Node) error {
		_, err := n.CreateVLAN(ctx, 200, VLANConfig{Description: "macvpn vlan"})
		return err
	}},
	{"create-vlan (with vni)", func(ctx context.Context, n *Node) error {
		_, err := n.CreateVLAN(ctx, 300, VLANConfig{Description: "vni vlan", L2VNI: 10300})
		return err
	}},
	{"bind-macvpn", func(ctx context.Context, n *Node) error {
		_, err := n.BindMACVPN(ctx, 200, "SERVERS")
		return err
	}},
	{"create-vrf (ipvpn target)", func(ctx context.Context, n *Node) error {
		_, err := n.CreateVRF(ctx, "Vrf_CUST", VRFConfig{})
		return err
	}},
	{"bind-ipvpn", func(ctx context.Context, n *Node) error {
		_, err := n.BindIPVPN(ctx, "CUST")
		return err
	}},
	{"create-portchannel", func(ctx context.Context, n *Node) error {
		_, err := n.CreatePortChannel(ctx, "PortChannel10", PortChannelConfig{
			Members:  []string{"Ethernet8"},
			MTU:      9100,
			MinLinks: 1,
			Fallback: true,
			FastRate: true,
		})
		return err
	}},
	{"add-pc-member", func(ctx context.Context, n *Node) error {
		_, err := n.AddPortChannelMember(ctx, "PortChannel10", "Ethernet12")
		return err
	}},
	{"create-acl", func(ctx context.Context, n *Node) error {
		_, err := n.CreateACL(ctx, "EDGE_IN", ACLConfig{
			Type:        "L3",
			Stage:       "ingress",
			Description: "edge ingress",
		})
		return err
	}},
	{"add-acl-rule", func(ctx context.Context, n *Node) error {
		_, err := n.AddACLRule(ctx, "EDGE_IN", "RULE_10", ACLRuleConfig{
			Priority: 100,
			Action:   "FORWARD",
			SrcIP:    "10.0.0.0/8",
			DstIP:    "192.168.0.0/16",
			Protocol: "6",
			SrcPort:  "1024-65535",
			DstPort:  "443",
		})
		return err
	}},
	{"configure-irb", func(ctx context.Context, n *Node) error {
		_, err := n.ConfigureIRB(ctx, 100, IRBConfig{
			VRF:        "Vrf_TEST",
			IPAddress:  "192.168.100.1/24",
			AnycastMAC: "00:00:00:aa:bb:cc",
		})
		return err
	}},
	{"add-static-route", func(ctx context.Context, n *Node) error {
		_, err := n.AddStaticRoute(ctx, "Vrf_TEST", "10.9.0.0/24", "10.9.1.1", 50)
		return err
	}},
	{"add-bgp-evpn-peer", func(ctx context.Context, n *Node) error {
		_, err := n.AddBGPEVPNPeer(ctx, "10.0.0.9", 65009, "evpn overlay peer", true)
		return err
	}},
	{"configure-interface (routed)", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet0")
		if err != nil {
			return err
		}
		_, err = i.ConfigureInterface(ctx, InterfaceConfig{IP: "10.1.0.0/31"})
		return err
	}},
	{"add-bgp-peer", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet0")
		if err != nil {
			return err
		}
		_, err = i.AddBGPPeer(ctx, DirectBGPPeerConfig{
			NeighborIP:  "10.1.0.1",
			RemoteAS:    65099,
			Description: "underlay peer",
			Multihop:    2,
		})
		return err
	}},
	{"configure-interface (access)", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet4")
		if err != nil {
			return err
		}
		_, err = i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true})
		return err
	}},
	{"add-trunk-vlan", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet4")
		if err != nil {
			return err
		}
		_, err = i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200, Tagged: true})
		return err
	}},
	{"set-property", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet20")
		if err != nil {
			return err
		}
		_, err = i.SetProperty(ctx, "mtu", "9100")
		return err
	}},
	{"bind-acl", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet20")
		if err != nil {
			return err
		}
		_, err = i.BindACL(ctx, "EDGE_IN", "ingress")
		return err
	}},
	{"bind-qos", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet20")
		if err != nil {
			return err
		}
		policy, err := n.GetQoSPolicy("GOLD")
		if err != nil {
			return err
		}
		_, err = i.BindQoS(ctx, "GOLD", policy)
		return err
	}},
	{"apply-service", func(ctx context.Context, n *Node) error {
		i, err := iface(n, "Ethernet16")
		if err != nil {
			return err
		}
		_, err = i.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{
			IPAddress: "10.2.0.0/31",
			PeerAS:    65002,
			Params: map[string]string{
				"route_reflector_client": "true",
				"next_hop_self":          "true",
			},
		})
		return err
	}},
}

// normalizeIntentFields returns a copy with the DAG-link CSVs sorted: replay
// re-registers children in topo-sort order, which may legitimately differ
// from original creation order. Set membership is the contract, not order.
func normalizeIntentFields(fields map[string]string) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		if k == "_children" || k == "_parents" {
			parts := strings.Split(v, ",")
			sort.Strings(parts)
			v = strings.Join(parts, ",")
		}
		out[k] = v
	}
	return out
}

// normalizedIntentDB returns the node's intent table with DAG links normalized.
func normalizedIntentDB(n *Node) map[string]map[string]string {
	out := make(map[string]map[string]string, len(n.configDB.NewtronIntent))
	for res, fields := range n.configDB.NewtronIntent {
		out[res] = normalizeIntentFields(fields)
	}
	return out
}

// diffStringMaps reports per-key differences between two map[string]string.
func diffStringMaps(t *testing.T, label string, a, b map[string]string) {
	t.Helper()
	for k, av := range a {
		if bv, ok := b[k]; !ok {
			t.Errorf("%s: field %q lost in round trip (was %q)", label, k, av)
		} else if av != bv {
			t.Errorf("%s: field %q changed in round trip: %q → %q", label, k, av, bv)
		}
	}
	for k, bv := range b {
		if _, ok := a[k]; !ok {
			t.Errorf("%s: field %q appeared only after replay (= %q)", label, k, bv)
		}
	}
}

// TestOpRoundTrip drives every replayable operation, exports the intent DB to
// topology steps, replays the steps onto a fresh identical node, and requires
// the two nodes to be indistinguishable: identical intent DBs and identical
// projections. This is the machine form of CLAUDE.md's round-trip checklist.
func TestOpRoundTrip(t *testing.T) {
	ctx := context.Background()

	// Side A: live operations.
	nA := roundTripNode()
	for _, inv := range roundTripSequence {
		if err := inv.invoke(ctx, nA); err != nil {
			t.Fatalf("op %q: %v", inv.op, err)
		}
	}

	// Coverage guard — a green run is meaningless if the sequence silently
	// stopped exercising an op (§16 honest tests). The intent DB must contain
	// every replayable operation plus the two side-effect ops; an op added to
	// the codebase without joining this sequence fails here.
	expectedOps := map[string]bool{
		"setup-device": true, "create-vrf": true, "create-vlan": true,
		"bind-macvpn": true, "bind-ipvpn": true, "create-portchannel": true,
		"add-pc-member": true, "create-acl": true, "add-acl-rule": true,
		"configure-irb": true, "add-static-route": true, "add-bgp-evpn-peer": true,
		"configure-interface": true, "add-trunk-vlan": true, "add-bgp-peer": true,
		"set-property": true, "bind-acl": true, "bind-qos": true, "apply-service": true,
		// Side-effect intents, re-created by their parents during replay:
		"interface-init": true, "deploy-service": true,
	}
	seenOps := map[string]bool{}
	for _, fields := range nA.configDB.NewtronIntent {
		seenOps[fields["operation"]] = true
	}
	for op := range expectedOps {
		if !seenOps[op] {
			t.Errorf("coverage: op %q was never recorded by the sequence", op)
		}
	}
	for op := range seenOps {
		if !expectedOps[op] {
			t.Errorf("coverage: op %q recorded but not in the expected set — extend expectedOps and the sequence's param coverage", op)
		}
	}

	// Manifest check — every stored intent param must be declared in its
	// operation's registry manifest (undeclared = the scattered-knowledge bug
	// class returning), and every Required param must be present. Meta fields
	// (operation, state, DAG links) are the intent envelope, not params.
	metaFields := map[string]bool{"operation": true, "state": true, "_parents": true, "_children": true}
	for resource, fields := range nA.configDB.NewtronIntent {
		op := fields["operation"]
		opSpec := opRegistry[op]
		if opSpec == nil {
			t.Errorf("intent %q: operation %q is not in the registry", resource, op)
			continue
		}
		declared := map[string]bool{}
		for _, ps := range opSpec.Params {
			declared[ps.Key] = true
		}
		if !opSpec.OpenParams {
			for k := range fields {
				if !metaFields[k] && !declared[k] {
					t.Errorf("intent %q (op %s): stored param %q is not declared in the registry manifest", resource, op, k)
				}
			}
		}
		for _, ps := range opSpec.Params {
			if ps.Required {
				if _, ok := fields[ps.Key]; !ok {
					t.Errorf("intent %q (op %s): required param %q missing", resource, op, ps.Key)
				}
			}
		}
	}

	// Export: intent DB → ordered steps.
	steps := IntentsToSteps(nA.configDB.NewtronIntent)
	if len(steps) == 0 {
		t.Fatal("IntentsToSteps produced no steps")
	}

	// Side B: reconstruction by replay.
	nB := roundTripNode()
	for _, s := range steps {
		if err := ReplayStep(ctx, nB, s); err != nil {
			t.Fatalf("replay %s %v: %v", s.URL, s.Params, err)
		}
	}

	// (a) Intent DB equality — params round-trip exactly, per resource.
	intentA := normalizedIntentDB(nA)
	intentB := normalizedIntentDB(nB)
	for res, fa := range intentA {
		fb, ok := intentB[res]
		if !ok {
			t.Errorf("intent %q lost in round trip", res)
			continue
		}
		diffStringMaps(t, "intent "+res, fa, fb)
	}
	for res := range intentB {
		if _, ok := intentA[res]; !ok {
			t.Errorf("intent %q appeared only after replay", res)
		}
	}

	// (b) Projection equality — the reconstructed CONFIG_DB is the original.
	rawA := nA.configDB.ExportRaw()
	rawB := nB.configDB.ExportRaw()
	for table, keysA := range rawA {
		keysB := rawB[table]
		for key, fieldsA := range keysA {
			fieldsB, ok := keysB[key]
			if !ok {
				t.Errorf("projection %s|%s lost in round trip", table, key)
				continue
			}
			if table == "NEWTRON_INTENT" {
				fieldsA = normalizeIntentFields(fieldsA)
				fieldsB = normalizeIntentFields(fieldsB)
			}
			diffStringMaps(t, "projection "+table+"|"+key, fieldsA, fieldsB)
		}
		for key := range keysB {
			if _, ok := keysA[key]; !ok {
				t.Errorf("projection %s|%s appeared only after replay", table, key)
			}
		}
	}
	for table := range rawB {
		if _, ok := rawA[table]; !ok {
			t.Errorf("projection table %s appeared only after replay", table)
		}
	}

	// (c) Export idempotence — steps from the reconstructed node equal the
	// steps that built it (a second save/rebuild cycle changes nothing).
	stepsB := IntentsToSteps(nB.configDB.NewtronIntent)
	if !reflect.DeepEqual(steps, stepsB) {
		t.Errorf("export not idempotent: %d steps then %d steps", len(steps), len(stepsB))
		for i := range steps {
			if i < len(stepsB) && !reflect.DeepEqual(steps[i], stepsB[i]) {
				t.Errorf("  step %d: %s %v  →  %s %v", i, steps[i].URL, steps[i].Params, stepsB[i].URL, stepsB[i].Params)
			}
		}
	}
}

// TestOpRegistrySanity checks the registry's internal invariants statically:
// map keys match entry names, every replayable entry has a Replay func and a
// declared §15 inverse, side-effect entries have neither a Replay nor an
// export path of their own.
func TestOpRegistrySanity(t *testing.T) {
	for key, opSpec := range opRegistry {
		if opSpec.Op != key {
			t.Errorf("registry key %q != entry Op %q", key, opSpec.Op)
		}
		if opSpec.SideEffect {
			if opSpec.Replay != nil {
				t.Errorf("%s: side-effect op must not have a Replay func", key)
			}
			continue
		}
		if opSpec.Replay == nil {
			t.Errorf("%s: replayable op missing Replay func", key)
		}
		if opSpec.Inverse == "" {
			t.Errorf("%s: missing §15 inverse declaration", key)
		}
		if len(opSpec.Params) == 0 && !opSpec.OpenParams {
			t.Errorf("%s: no params declared and not OpenParams — an op with no manifest cannot be checked", key)
		}
	}
}

// TestReplace_SkipsUnchangedRows pins §48's "unchanged sibling rows stay
// untouched": an update whose regenerated sibling row (BGP_NEIGHBOR_AF) is
// identical to the projection must emit NO change for that row — an
// identical-fields HSET still fires a keyspace notification, and sonic-vs
// frrcfgd's runtime AF handling deactivates the address family on reprocess
// (RCA-049; caught on the wire by the evpn continuity check).
func TestReplace_SkipsUnchangedRows(t *testing.T) {
	ctx := context.Background()
	n := testDevice()

	// Establish the peer: BGP_NEIGHBOR + identical-forever AF row.
	if _, err := n.SetupDevice(ctx, SetupDeviceOpts{
		Fields: map[string]string{"hostname": "t", "bgp_asn": "65001"}, SourceIP: "10.255.0.1",
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := n.AddBGPEVPNPeer(ctx, "10.9.9.2", 65002, "before", true); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Description-only update: BGP_NEIGHBOR changes, the AF row does not.
	cs, err := n.UpdateBGPEVPNPeer(ctx, "10.9.9.2", 65002, "after", true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	c := assertChange(t, cs, "BGP_NEIGHBOR", "default|10.9.9.2", ChangeReplace)
	assertField(t, c, "name", "after")
	assertNoChangeOfType(t, cs, "BGP_NEIGHBOR_AF", "default|10.9.9.2|l2vpn_evpn", ChangeReplace)
	assertNoChangeOfType(t, cs, "BGP_NEIGHBOR_AF", "default|10.9.9.2|l2vpn_evpn", ChangeDelete)
}

// TestAddBGPEVPNPeer_RefusesProfileOwnedRow pins the §27 single-owner leg of
// BGPNeighborExists: a BGP_NEIGHBOR row created by a device-intent
// sub-operation (profile-driven overlay — no discrete evpn-peer| intent)
// must refuse a second owner. Without it, add silently merged onto the
// fabric's row and remove then amputated it, leaving phantom drift
// (RCA-049 addendum).
func TestAddBGPEVPNPeer_RefusesProfileOwnedRow(t *testing.T) {
	ctx := context.Background()
	n := testDevice()
	if _, err := n.SetupDevice(ctx, SetupDeviceOpts{
		Fields: map[string]string{"hostname": "t", "bgp_asn": "65001"}, SourceIP: "10.255.0.1",
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Simulate a profile-owned overlay peer: a projection row with no
	// discrete evpn-peer| intent — exactly what ConfigureBGPOverlay's
	// sub-operation leaves behind.
	n.configDB.BGPNeighbor["default|10.0.0.99"] = sonic.BGPNeighborEntry{ASN: "65099"}

	if _, err := n.AddBGPEVPNPeer(ctx, "10.0.0.99", 65099, "dup", true); err == nil {
		t.Fatal("AddBGPEVPNPeer over a profile-owned row must be refused")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want already-exists refusal, got: %v", err)
	}
}
