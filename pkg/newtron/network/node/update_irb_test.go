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

// TestSVISingleAuthorGuards pins §27 both ways: configure-irb refuses a
// service-owned SVI, and an irb-type service refuses an operator-authored
// one. Two writers of one gateway diverge on the first refresh.
func TestSVISingleAuthorGuards(t *testing.T) {
	ctx := context.Background()

	// Direction 1: operator-authored IRB exists → irb-type service refused.
	// (Driven through the service path's guard by seeding the spec.)
	n := irbNode(t, IRBConfig{IPAddress: "10.1.100.1/24"})
	sp := n.SpecProvider.(*testSpecProvider)
	sp.services["cust-irb"] = irbServiceSpec()
	intf, err := n.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	_, err = intf.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"})
	if err == nil || !strings.Contains(err.Error(), "operator-authored") {
		t.Fatalf("irb-type service must refuse an operator-authored SVI, got %v", err)
	}

	// Direction 2: service-owned SVI → configure-irb (and update-irb) refused.
	n2, _ := testInterface()
	sp2 := n2.SpecProvider.(*testSpecProvider)
	sp2.services["cust-irb"] = irbServiceSpec()
	intf2, err := n2.GetInterface("Ethernet0")
	if err != nil {
		t.Fatalf("GetInterface: %v", err)
	}
	if _, err := intf2.ApplyService(ctx, "cust-irb", ApplyServiceOpts{VLAN: 100, IPAddress: "10.1.100.1/24"}); err != nil {
		t.Fatalf("ApplyService(irb) on clean VLAN: %v", err)
	}
	_, err = n2.ConfigureIRB(ctx, 100, IRBConfig{IPAddress: "10.1.100.9/24"})
	if err == nil || !strings.Contains(err.Error(), "owned by service") {
		t.Fatalf("configure-irb must refuse a service-owned SVI, got %v", err)
	}
}

// irbServiceSpec is a minimal local irb-type service for the guard tests —
// VLAN + gateway IP supplied at apply time, no overlay references.
func irbServiceSpec() *spec.ServiceSpec {
	return &spec.ServiceSpec{ServiceType: spec.ServiceTypeIRB}
}
