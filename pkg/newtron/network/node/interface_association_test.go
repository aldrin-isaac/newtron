package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// A port's identity record and its VLAN/routed association are separate records
// (#535). Whatever is put on the port first, joining a VLAN afterwards works:
// the identity never has to change its parents.
func TestAssociation_AttributesThenVLANJoin(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatal(err)
	}
	i, _ := n.GetInterface("Ethernet0")
	if _, err := i.SetProperty(ctx, "mtu", "9000"); err != nil {
		t.Fatalf("SetProperty: %v", err)
	}
	if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("join VLAN 100 after a property: %v", err)
	}
	member := n.GetIntent(vlanMembershipKey("Ethernet0", 100))
	if member == nil {
		t.Fatal("membership record missing")
	}
	if !equalSet(member.Parents, []string{"interface|Ethernet0", "vlan|100"}) {
		t.Errorf("membership parents = %v, want the identity and the VLAN", member.Parents)
	}
	if identity := n.GetIntent("interface|Ethernet0"); identity == nil || !equalSet(identity.Parents, []string{"device"}) {
		t.Errorf("identity = %+v, want an interface-init record parented on device", identity)
	}
}

// Removing a bridged service removes the port's VLAN membership even when the
// port carries other records; the identity stays while they do (#535).
func TestAssociation_BridgedTeardownWithAttributes(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()
	sp := n.SpecProvider.(*testSpecProvider)
	sp.macvpn["L2"] = &spec.MACVPNSpec{VlanID: 300}
	sp.services["BRIDGED"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged, MACVPN: "L2"}

	i, _ := n.GetInterface("Ethernet0")
	if _, err := i.ApplyService(ctx, "BRIDGED", ApplyServiceOpts{}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}
	if _, err := i.SetProperty(ctx, "mtu", "9000"); err != nil {
		t.Fatalf("SetProperty: %v", err)
	}
	if _, err := i.RemoveService(ctx); err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	if n.GetIntent(vlanMembershipKey("Ethernet0", 300)) != nil {
		t.Error("the service's VLAN membership survived remove-service")
	}
	if _, ok := n.ConfigDB().VLANMember["Vlan300|Ethernet0"]; ok {
		t.Error("VLAN_MEMBER row survived remove-service")
	}
	if n.GetIntent("interface|Ethernet0") == nil {
		t.Error("identity removed while the mtu property still hangs under it")
	}
	if _, err := i.ClearProperty(ctx, "mtu"); err != nil {
		t.Fatalf("ClearProperty: %v", err)
	}
	if n.GetIntent("interface|Ethernet0") != nil {
		t.Error("identity should go with the port's last record")
	}
}

// A VLAN cannot be deleted while a port is a member: the membership record is
// the VLAN's child (I5).
func TestAssociation_VLANProtectedByMembership(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatal(err)
	}
	i, _ := n.GetInterface("Ethernet0")
	if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.DeleteVLAN(ctx, 100); err == nil || !strings.Contains(err.Error(), vlanMembershipKey("Ethernet0", 100)) {
		t.Fatalf("DeleteVLAN = %v, want a refusal naming the membership", err)
	}
	if _, err := i.UnconfigureInterface(ctx); err != nil {
		t.Fatalf("UnconfigureInterface: %v", err)
	}
	if _, err := n.DeleteVLAN(ctx, 100); err != nil {
		t.Fatalf("DeleteVLAN after the member left: %v", err)
	}
}

// An interface has at most one untagged VLAN, is bridged or routed but not both,
// and is a tagged or an untagged member of a VLAN, not both.
func TestAssociation_Refusals(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*Node, *Interface) {
		n := newTestAbstractNode()
		for _, v := range []int{100, 200} {
			if _, err := n.CreateVLAN(ctx, v, VLANConfig{}); err != nil {
				t.Fatal(err)
			}
		}
		i, _ := n.GetInterface("Ethernet0")
		return n, i
	}
	t.Run("second untagged VLAN", func(t *testing.T) {
		_, i := setup(t)
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
			t.Fatal(err)
		}
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200}); err == nil {
			t.Error("a second untagged VLAN was accepted")
		}
	})
	t.Run("untagged plus tagged in other VLANs", func(t *testing.T) {
		_, i := setup(t)
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
			t.Fatal(err)
		}
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 200, Tagged: true}); err != nil {
			t.Errorf("a native VLAN plus a tagged VLAN must be allowed: %v", err)
		}
	})
	t.Run("tagged and untagged in the same VLAN", func(t *testing.T) {
		_, i := setup(t)
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err == nil {
			t.Error("flipping VLAN 100 from tagged to untagged was accepted")
		}
	})
	t.Run("routed then VLAN", func(t *testing.T) {
		_, i := setup(t)
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{IP: "10.0.0.0/31"}); err != nil {
			t.Fatal(err)
		}
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err == nil {
			t.Error("a routed interface joined a VLAN")
		}
	})
	t.Run("routed VRF change", func(t *testing.T) {
		n, i := setup(t)
		for _, v := range []string{"Vrf_A", "Vrf_B"} {
			if _, err := n.CreateVRF(ctx, v, VRFConfig{}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_A", IP: "10.0.0.0/31"}); err != nil {
			t.Fatal(err)
		}
		_, err := i.ConfigureInterface(ctx, InterfaceConfig{VRF: "Vrf_B", IP: "10.0.0.0/31"})
		if !errors.Is(err, util.ErrPreconditionFailed) {
			t.Errorf("moving a routed interface to another VRF: err = %v, want a precondition refusal", err)
		}
	})
	t.Run("VLAN then routed", func(t *testing.T) {
		_, i := setup(t)
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100, Tagged: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := i.ConfigureInterface(ctx, InterfaceConfig{IP: "10.0.0.0/31"}); err == nil {
			t.Error("a VLAN member was routed")
		}
	})
}

// A BGP peer depends on the IP that supplies its update-source, so it is a child
// of the routed association: replay orders it after the IP, and unconfigure
// removes it before the association.
func TestAssociation_BGPPeerFollowsRoutedIP(t *testing.T) {
	ctx := context.Background()
	n := newTestAbstractNode()
	i, _ := n.GetInterface("Ethernet0")
	if _, err := i.ConfigureInterface(ctx, InterfaceConfig{IP: "10.1.0.0/31"}); err != nil {
		t.Fatal(err)
	}
	if _, err := i.AddBGPPeer(ctx, DirectBGPPeerConfig{NeighborIP: "10.1.0.1", RemoteAS: 65099}); err != nil {
		t.Fatalf("AddBGPPeer: %v", err)
	}
	peer := n.GetIntent("interface|Ethernet0|bgp-peer")
	if peer == nil || !equalSet(peer.Parents, []string{"interface|Ethernet0", routedKey("Ethernet0")}) {
		t.Fatalf("peer = %+v, want parents [identity, routed association]", peer)
	}
	steps := IntentsToSteps(n.configDB.NewtronIntent)
	routedAt, peerAt := -1, -1
	for k, s := range steps {
		switch {
		case strings.HasSuffix(s.URL, "/add-bgp-peer"):
			peerAt = k
		case strings.HasSuffix(s.URL, "/configure-interface") && s.Params["ip"] == "10.1.0.0/31":
			routedAt = k
		}
	}
	if routedAt < 0 || peerAt < routedAt {
		t.Errorf("replay order: routed at %d, peer at %d — the peer must follow its IP", routedAt, peerAt)
	}
	if _, err := i.UnconfigureInterface(ctx); err != nil {
		t.Fatalf("UnconfigureInterface: %v", err)
	}
	if n.GetIntent("interface|Ethernet0") != nil {
		t.Error("unconfigure-interface left the identity record")
	}
}

func equalSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}
