package node

import (
	"context"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ============================================================================
// Capability-gate fences.
//
// Fence 1 (registry conformance): every ScopeInterface forward op must
// declare its capability needs — statically via OpSpec.Needs, or by
// appearing in contentDerivedOps with an in-method check. An op in neither
// set would run ungated on every kind; this test makes that a compile-time-
// adjacent failure instead of a silent hole.
//
// Fence 2 (the matrix, end to end): drives real ops against a physical
// port, a PortChannel, and an IRB on a loopback node, asserting allow vs
// refuse cell-by-cell — including the refusal text (the configure-irb
// redirect), so a message regression is also a test failure.
// ============================================================================

func TestOpRegistryInterfaceOpsDeclareNeeds(t *testing.T) {
	for verb, op := range opRegistry {
		if op.Scope != ScopeInterface || op.SideEffect {
			continue
		}
		if len(op.Needs) == 0 && !contentDerivedOps[verb] {
			t.Errorf("%s: ScopeInterface op declares no Needs and is not content-derived — classify it (OpSpec.Needs or contentDerivedOps)", verb)
		}
		if len(op.Needs) > 0 && contentDerivedOps[verb] {
			t.Errorf("%s: declares both static Needs and content-derived — pick one", verb)
		}
	}
	// contentDerivedOps must not name ops that left the registry.
	for verb := range contentDerivedOps {
		if _, ok := opRegistry[verb]; !ok {
			t.Errorf("contentDerivedOps names %q which is not a registered op", verb)
		}
	}
}

// isCapabilityRefusal distinguishes gate refusals from other op errors —
// the two refusal forms RequireInterfaceCapabilities produces.
func isCapabilityRefusal(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "does not support") ||
		strings.Contains(err.Error(), "is authored via")
}

// gateMatrixNode builds a loopback node carrying all three gateable kinds:
// Ethernet0 (physical, from the fixture), PortChannel1, and Vlan100 (IRB).
func gateMatrixNode(t *testing.T) *Node {
	t.Helper()
	n, _ := testInterface()
	ctx := context.Background()
	if _, err := n.CreatePortChannel(ctx, "PortChannel1", PortChannelConfig{}); err != nil {
		t.Fatalf("CreatePortChannel: %v", err)
	}
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	return n
}

func TestCapabilityGateMatrix(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		target  string
		run     func(*Node, *Interface) error
		refused bool
		redirect string // required substring of a refusal (e.g. the configure-irb pointer)
	}{
		// --- bind-acl: ACL binding is PORT ∪ PORTCHANNEL (sonic-acl.yang) ---
		{"bind-acl on Ethernet passes gate", "Ethernet0", func(n *Node, i *Interface) error {
			_, err := i.BindACL(ctx, "NOSUCH", "ingress")
			return err
		}, false, ""},
		{"bind-acl on PortChannel passes gate", "PortChannel1", func(n *Node, i *Interface) error {
			_, err := i.BindACL(ctx, "NOSUCH", "ingress")
			return err
		}, false, ""},
		{"bind-acl on IRB refused", "Vlan100", func(n *Node, i *Interface) error {
			_, err := i.BindACL(ctx, "NOSUCH", "ingress")
			return err
		}, true, "ACL binding"},

		// --- bind-qos: PORT_QOS_MAP is global|PORT (sonic-port-qos-map.yang) ---
		{"bind-qos on PortChannel refused", "PortChannel1", func(n *Node, i *Interface) error {
			_, err := i.BindQoS(ctx, "nosuch", &spec.QoSPolicy{})
			return err
		}, true, "QoS binding"},
		{"bind-qos on IRB refused", "Vlan100", func(n *Node, i *Interface) error {
			_, err := i.BindQoS(ctx, "nosuch", &spec.QoSPolicy{})
			return err
		}, true, "QoS binding"},

		// --- set-property: per-property granularity ---
		{"set-property admin_status on PortChannel passes gate", "PortChannel1", func(n *Node, i *Interface) error {
			_, err := i.SetProperty(ctx, "admin_status", "down")
			return err
		}, false, ""},
		{"set-property speed on PortChannel refused", "PortChannel1", func(n *Node, i *Interface) error {
			_, err := i.SetProperty(ctx, "speed", "100G")
			return err
		}, true, ""},
		{"set-property on IRB refused at kind level", "Vlan100", func(n *Node, i *Interface) error {
			_, err := i.SetProperty(ctx, "mtu", "9100")
			return err
		}, true, "port properties"},

		// --- configure-interface: content-derived ---
		{"configure-interface routed on IRB redirects to configure-irb", "Vlan100", func(n *Node, i *Interface) error {
			_, err := i.ConfigureInterface(ctx, InterfaceConfig{IP: "10.0.0.1/24"})
			return err
		}, true, "configure-irb"},
		{"configure-interface bridged on IRB refused", "Vlan100", func(n *Node, i *Interface) error {
			_, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100})
			return err
		}, true, "VLAN membership"},
		{"configure-interface routed on PortChannel passes gate", "PortChannel1", func(n *Node, i *Interface) error {
			_, err := i.ConfigureInterface(ctx, InterfaceConfig{IP: "10.30.0.0/31"})
			return err
		}, false, ""},

		// --- BGP peering: allowed on all three kinds (the IRB's headline op) ---
		{"add-bgp-peer on IRB passes gate", "Vlan100", func(n *Node, i *Interface) error {
			_, err := i.AddBGPPeer(ctx, DirectBGPPeerConfig{RemoteAS: 65002})
			return err // fails later on "no IP address" — NOT a capability refusal
		}, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := gateMatrixNode(t)
			intf, err := n.GetInterface(tt.target)
			if err != nil {
				t.Fatalf("GetInterface(%s): %v", tt.target, err)
			}
			err = tt.run(n, intf)
			if tt.refused {
				if !isCapabilityRefusal(err) && (tt.redirect == "" && err == nil) {
					t.Fatalf("want capability refusal, got %v", err)
				}
				if err == nil {
					t.Fatal("want refusal, got nil")
				}
				if tt.redirect != "" && !strings.Contains(err.Error(), tt.redirect) {
					t.Fatalf("refusal %q does not name %q", err.Error(), tt.redirect)
				}
			} else if isCapabilityRefusal(err) {
				t.Fatalf("gate refused an allowed cell: %v", err)
			}
		})
	}
}

// TestBGPPeerOnConfiguredIRB proves the full positive IRB cell: configure-irb
// gives the SVI an IP, then add-bgp-peer on the Vlan interface succeeds —
// the classic gateway-peering flow, in loopback.
func TestBGPPeerOnConfiguredIRB(t *testing.T) {
	ctx := context.Background()
	n := gateMatrixNode(t)
	if _, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.100.0.0/31"}); err != nil {
		t.Fatalf("ConfigureIRB: %v", err)
	}
	intf, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface(Vlan100): %v", err)
	}
	if _, err := intf.AddBGPPeer(ctx, DirectBGPPeerConfig{RemoteAS: 65002}); err != nil {
		t.Fatalf("AddBGPPeer on configured IRB: %v", err)
	}
	if n.GetIntent("interface|Vlan100|bgp-peer") == nil {
		t.Fatal("bgp-peer intent missing after AddBGPPeer on IRB")
	}
}
