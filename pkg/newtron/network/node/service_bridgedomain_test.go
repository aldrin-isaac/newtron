package node

import (
	"context"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestApplyService_RequiresBridgeDomain pins DE-1: a bridged or irb service
// delivers onto a pre-existing bridge domain and no longer creates it. The
// VLAN and the interface's membership are authored separately (create-vlan,
// configure-interface); the service requires them.
func TestApplyService_RequiresBridgeDomain(t *testing.T) {
	ctx := context.Background()

	t.Run("refused when VLAN absent", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
		_, err := intf.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"})
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("want VLAN-absent refusal, got %v", err)
		}
	})

	t.Run("refused when not a member", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN: %v", err)
		}
		_, err := intf.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"})
		if err == nil || !strings.Contains(err.Error(), "not a member") {
			t.Fatalf("want not-a-member refusal pointing at configure-interface, got %v", err)
		}
		if !strings.Contains(err.Error(), "configure-interface") {
			t.Fatalf("refusal should name configure-interface: %v", err)
		}
	})

	t.Run("succeeds onto a pre-existing bridge domain, creates no membership", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN: %v", err)
		}
		memberCS, err := intf.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100})
		if err != nil {
			t.Fatalf("ConfigureInterface (membership): %v", err)
		}
		// The membership rows come from configure-interface, not the service.
		assertChange(t, memberCS, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeAdd)

		svcCS, err := intf.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"})
		if err != nil {
			t.Fatalf("ApplyService onto a pre-existing bridge domain: %v", err)
		}
		// The service delivers the SVI gateway...
		assertChange(t, svcCS, "VLAN_INTERFACE", "Vlan100", ChangeAdd)
		// ...but NOT membership: VLAN_MEMBER is configure-interface's alone (§6).
		assertNoChange(t, svcCS, "VLAN_MEMBER", "Vlan100|Ethernet0")
	})
}

// TestRemoveService_LeavesBridgeDomain pins the teardown half of DE-1: removing
// an irb service deletes its SVI but leaves the VLAN and the membership, which
// it never owned.
func TestRemoveService_LeavesBridgeDomain(t *testing.T) {
	ctx := context.Background()
	n, intf := testInterface()
	n.SpecProvider.(*testSpecProvider).services["cust-irb"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := intf.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100}); err != nil {
		t.Fatalf("ConfigureInterface: %v", err)
	}
	if _, err := intf.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	// SVI gateway torn down (last irb-service user)...
	assertChange(t, cs, "VLAN_INTERFACE", "Vlan100", ChangeDelete)
	// ...but the VLAN and the membership stay — the service never owned them.
	assertNoChange(t, cs, "VLAN", "Vlan100")
	assertNoChange(t, cs, "VLAN_MEMBER", "Vlan100|Ethernet0")
	if n.GetIntent("vlan|100") == nil {
		t.Fatal("VLAN intent must survive service removal (create-vlan owns it)")
	}
}
