package node

import (
	"context"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestApplyService_BridgedRequiresMembership pins the bridge-domain precondition
// for the per-access-port service types (DE-1): a bridged service delivers onto
// an access port that must already be a member of a pre-existing VLAN. The VLAN
// and the membership are authored separately (create-vlan, configure-interface),
// each with one owner (§6); the service requires them and creates neither.
func TestApplyService_BridgedRequiresMembership(t *testing.T) {
	ctx := context.Background()

	t.Run("refused when VLAN absent", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
		_, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100})
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("want VLAN-absent refusal, got %v", err)
		}
	})

	t.Run("refused when not a member", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN: %v", err)
		}
		_, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100})
		if err == nil || !strings.Contains(err.Error(), "not a member") {
			t.Fatalf("want not-a-member refusal, got %v", err)
		}
		if !strings.Contains(err.Error(), "configure-interface") {
			t.Fatalf("refusal should name configure-interface: %v", err)
		}
	})

	t.Run("succeeds onto a pre-existing bridge domain, creates no membership", func(t *testing.T) {
		n, intf := testInterface()
		n.SpecProvider.(*testSpecProvider).services["cust-bridged"] = &spec.ServiceSpec{ServiceType: spec.ServiceTypeBridged}
		if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
			t.Fatalf("CreateVLAN: %v", err)
		}
		memberCS, err := intf.ConfigureInterface(ctx, InterfaceConfig{VLAN: 100})
		if err != nil {
			t.Fatalf("ConfigureInterface (membership): %v", err)
		}
		// The membership rows come from configure-interface, not the service.
		assertChange(t, memberCS, "VLAN_MEMBER", "Vlan100|Ethernet0", ChangeAdd)

		svcCS, err := intf.ApplyService(ctx, "cust-bridged", ApplyServiceOpts{VLAN: 100})
		if err != nil {
			t.Fatalf("ApplyService onto a pre-existing bridge domain: %v", err)
		}
		// The service creates NO membership: VLAN_MEMBER is configure-interface's (§6).
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

// TestRemoveService_IRBLeavesGateway pins the teardown half of the flip: removing
// an irb service deletes its binding but leaves the SVI gateway, which
// configure-irb owns (§6). The gateway is torn down by unconfigure-irb, not
// remove-service — and it survives reconstruction (no drift on rebuild).
func TestRemoveService_IRBLeavesGateway(t *testing.T) {
	ctx := context.Background()
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
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := irb.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100}); err != nil {
		t.Fatalf("ApplyService: %v", err)
	}

	cs, err := irb.RemoveService(ctx)
	if err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	// The SVI gateway stays — configure-irb owns it, unconfigure-irb removes it (§6).
	assertNoChange(t, cs, "VLAN_INTERFACE", "Vlan100")
	// The binding is gone...
	if n.GetIntent(bindingKey("Vlan100")) != nil {
		t.Fatal("service binding must be deleted by remove-service")
	}
	// ...but the IRB identity (configure-irb) survives — the SVI has an owner.
	if irbIntent := n.GetIntent("interface|Vlan100"); irbIntent == nil || irbIntent.Operation != sonic.OpConfigureIRB {
		t.Fatalf("configure-irb identity (the SVI) must survive service removal, got %+v", irbIntent)
	}
	// The SVI survives reconstruction — no drift on the device.
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
	if _, ok := n.configDB.VLANInterface["Vlan100"]; !ok {
		t.Fatal("SVI must survive in the rebuilt projection — configure-irb owns it")
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
