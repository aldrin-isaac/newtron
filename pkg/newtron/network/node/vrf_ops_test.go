package node

import (
	"context"
	"strings"
	"testing"
)

// TestUnbindIPVPN_SelfReferenceExcluded guards the reference scan in
// UnbindIPVPN: the ipvpn intent records its own vrf_name, so a naive
// "any intent referencing this VRF" scan matches the binding being torn down
// and would refuse every standalone unbind. The scan must exclude the ipvpn
// intent itself and count only OTHER consumers (service bindings).
func TestUnbindIPVPN_SelfReferenceExcluded(t *testing.T) {
	ctx := context.Background()

	setup := func() *Node {
		n := testDevice()
		n.configDB.NewtronIntent["device"] = map[string]string{
			"operation": "setup-device", "state": "actuated",
			"_children": "vrf|Vrf_STANDALONE",
		}
		n.configDB.NewtronIntent["vrf|Vrf_STANDALONE"] = map[string]string{
			"operation": "create-vrf", "name": "Vrf_STANDALONE", "state": "actuated",
			"_parents": "device", "_children": "ipvpn|IPVPN3",
		}
		n.configDB.NewtronIntent["ipvpn|IPVPN3"] = map[string]string{
			"operation": "bind-ipvpn", "ipvpn": "IPVPN3", "vrf_name": "Vrf_STANDALONE",
			"l3vni": "10003", "l3vni_vlan": "0", "route_targets": "65001:303",
			"state": "actuated", "_parents": "vrf|Vrf_STANDALONE",
		}
		return n
	}

	// Only the ipvpn intent itself references Vrf_STANDALONE → unbind proceeds.
	t.Run("no other consumer", func(t *testing.T) {
		n := setup()
		cs, err := n.UnbindIPVPN(ctx, "IPVPN3")
		if err != nil {
			t.Fatalf("UnbindIPVPN refused with only the self-reference present: %v", err)
		}
		assertChange(t, cs, "NEWTRON_INTENT", "ipvpn|IPVPN3", ChangeDelete)
	})

	// A service binding still routes in the VRF → unbind is refused.
	t.Run("service binding still references", func(t *testing.T) {
		n := setup()
		n.configDB.NewtronIntent["interface|Ethernet0|service"] = map[string]string{
			"operation": "apply-service", "service_name": "CUST", "vrf_name": "Vrf_STANDALONE",
			"state": "actuated", "_parents": "interface|Ethernet0",
		}
		_, err := n.UnbindIPVPN(ctx, "IPVPN3")
		if err == nil {
			t.Fatal("UnbindIPVPN should refuse while a service binding references the VRF")
		}
		if !strings.Contains(err.Error(), "still reference it") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
