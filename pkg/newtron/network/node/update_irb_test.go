package node

import (
	"context"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// irbNode builds a loopback node with VLAN 100 and an operator-authored IRB.
func irbNode(t *testing.T, opts IRBConfig) *Node {
	t.Helper()
	n, _ := testInterface()
	ctx := context.Background()
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	if _, err := n.ConfigureIRB(ctx, 100, opts); err != nil {
		t.Fatalf("ConfigureIRB: %v", err)
	}
	return n
}

// TestUpdateIRBGatewayIPMove proves the §48 delivery: the old IP sub-entry
// is gone, exactly one new sub-entry exists, and the SVI base row was never
// deleted — the ghost-gateway-IP failure mode, pinned in reverse.
func TestUpdateIRBGatewayIPMove(t *testing.T) {
	ctx := context.Background()
	n := irbNode(t, IRBConfig{IPAddress: "10.1.100.1/24"})

	if _, err := n.UpdateIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.254/24"}); err != nil {
		t.Fatalf("UpdateIRB: %v", err)
	}

	svi := n.ConfigDB().VLANInterface
	if _, ok := svi["Vlan100"]; !ok {
		t.Fatal("SVI base row missing — an in-place update must never touch it")
	}
	if _, ok := svi["Vlan100|10.1.100.1/24"]; ok {
		t.Fatal("old gateway IP sub-entry survived the update — ghost gateway")
	}
	if _, ok := svi["Vlan100|10.1.100.254/24"]; !ok {
		t.Fatal("new gateway IP sub-entry missing")
	}
	// Intent carries the updated identity for replay (§20).
	intent := n.GetIntent("interface|Vlan100")
	if intent == nil || intent.Params["ip_address"] != "10.1.100.254/24" {
		t.Fatalf("intent not updated: %+v", intent)
	}
}

func TestUpdateIRBRefusesVRFMove(t *testing.T) {
	ctx := context.Background()
	n := irbNode(t, IRBConfig{IPAddress: "10.1.100.1/24"})
	if _, err := n.CreateVRF(ctx, "Vrf_X", VRFConfig{}); err != nil {
		t.Fatalf("CreateVRF: %v", err)
	}
	_, err := n.UpdateIRB(ctx, 100, IRBConfig{VRF: "Vrf_X", IPAddress: "10.1.100.1/24"})
	if err == nil || !strings.Contains(err.Error(), "unconfigure-irb") {
		t.Fatalf("VRF move must be refused with the teardown path named, got %v", err)
	}
}

func TestUpdateIRBRequiresExistingIRB(t *testing.T) {
	ctx := context.Background()
	n, _ := testInterface()
	if _, err := n.CreateVLAN(ctx, 100, VLANConfig{}); err != nil {
		t.Fatalf("CreateVLAN: %v", err)
	}
	_, err := n.UpdateIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"})
	if err == nil || !strings.Contains(err.Error(), "configure-irb first") {
		t.Fatalf("update without an IRB must redirect to configure-irb, got %v", err)
	}
}

func TestUpdateIRBIdempotentNoOp(t *testing.T) {
	ctx := context.Background()
	n := irbNode(t, IRBConfig{IPAddress: "10.1.100.1/24"})
	cs, err := n.UpdateIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"})
	if err != nil {
		t.Fatalf("UpdateIRB same identity: %v", err)
	}
	if len(cs.Changes) != 0 {
		t.Fatalf("same-identity update must be a no-op, got %d changes", len(cs.Changes))
	}
}

// TestSVISingleAuthorStructural pins §6 under the delivery-point flip: the SVI
// has exactly one author — configure-irb — structurally, not defensively. An
// irb-type service binds on top of the operator-authored IRB without writing
// VLAN_INTERFACE, so there is no rival writer to guard against; and calling
// configure-irb again once the service is bound is an idempotent no-op (the IRB
// already exists), not a refusal. The pre-flip mutual-exclusion — where the
// service authored its own SVI and the two paths fought — is gone.
func TestSVISingleAuthorStructural(t *testing.T) {
	ctx := context.Background()

	// Operator authors the IRB (create-vlan + configure-irb), then the service
	// binds to it — the flipped order (irb-service-redesign.md §3).
	n := irbNode(t, IRBConfig{IPAddress: "10.1.100.1/24"})
	sp := n.SpecProvider.(*testSpecProvider)
	sp.services["cust-irb"] = irbServiceSpec()
	irb, err := n.GetInterface("Vlan100")
	if err != nil {
		t.Fatalf("GetInterface(Vlan100): %v", err)
	}
	svcCS, err := irb.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100})
	if err != nil {
		t.Fatalf("irb-type service must bind to the operator-authored IRB, got %v", err)
	}
	// The service does not touch the SVI — configure-irb is its sole author.
	assertNoChange(t, svcCS, "VLAN_INTERFACE", "Vlan100")

	// configure-irb again, now that the service is bound, is an idempotent
	// no-op (the IRB already exists) — not a "owned by service" refusal.
	cs, err := n.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.1/24"})
	if err != nil {
		t.Fatalf("re-configure-irb after service bind must be a no-op, got %v", err)
	}
	if len(cs.Changes) != 0 {
		t.Fatalf("re-configure-irb must be a no-op, got %d changes", len(cs.Changes))
	}

	// And unconfigure-irb is refused while the service is bound — the binding is
	// a DAG child of the IRB identity, so I5 blocks the teardown (§5): no
	// bespoke guard, the lifecycle rule falls out of the invariant.
	_, err = n.UnconfigureIRB(ctx, 100)
	if err == nil || !strings.Contains(err.Error(), "children") {
		t.Fatalf("unconfigure-irb must be refused (I5) while a service is bound, got %v", err)
	}
}

// irbServiceSpec is a minimal local irb-type service for the guard tests —
// VLAN + gateway IP supplied at apply time, no overlay references.
func irbServiceSpec() *spec.ServiceSpec {
	return &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
}
