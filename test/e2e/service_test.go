//go:build e2e

package e2e_test

import (
	"context"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/operations"
)

func TestE2E_ApplyServiceL2(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service-L2", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface for L2 service binding")
	}

	// L2 service with macvpn requires VTEP for VXLAN tunnel mapping
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; l2-extend requires EVPN")
	}

	// Apply l2-extend service (no IP needed for L2)
	op := operations.NewApplyServiceOp(targetIface, "l2-extend", "")
	if err := op.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "l2-extend service not available")
		t.Skipf("l2-extend service not available in lab specs: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Cleanup: remove service via fresh connection
	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		cleanIntf, err := d.GetInterface(targetIface)
		if err != nil {
			return nil, err
		}
		return cleanIntf.RemoveService(ctx)
	})

	// Redis DEL fallback for L2 artifacts
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "NEWTRON_SERVICE_BINDING|"+targetIface)
		client.Del(c, "VLAN_MEMBER|Vlan100|"+targetIface)
		client.Del(c, "VLAN|Vlan100")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_Vlan100")
		client.Del(c, "SUPPRESS_VLAN_NEIGH|Vlan100")
	})

	// Verify: VLAN created
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN", "Vlan100")

	// Verify: VLAN_MEMBER with untagged mode
	testutil.AssertConfigDBEntry(t, nodeName, "VLAN_MEMBER", "Vlan100|"+targetIface, map[string]string{
		"tagging_mode": "untagged",
	})

	// Verify: service binding
	testutil.AssertConfigDBEntry(t, nodeName, "NEWTRON_SERVICE_BINDING", targetIface, map[string]string{
		"service_name": "l2-extend",
	})

	// Verify VXLAN tunnel map if VTEP is configured
	verifyDev := testutil.LabConnectedDevice(t, nodeName)
	if verifyDev.VTEPExists() {
		testutil.AssertConfigDBEntryExists(t, nodeName, "VXLAN_TUNNEL_MAP", "vtep1|map_10100_Vlan100")
	}
}

func TestE2E_ApplyServiceIRB(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service-IRB", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface for IRB service binding")
	}

	// IRB service requires VTEP for both L2 VNI (macvpn) and L3 VNI (ipvpn)
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; server-irb requires EVPN")
	}

	// Apply server-irb service (shared VRF, anycast gateway)
	op := operations.NewApplyServiceOp(targetIface, "server-irb", "")
	if err := op.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "server-irb service not available")
		t.Skipf("server-irb service not available in lab specs: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Cleanup: remove service via fresh connection
	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		cleanIntf, err := d.GetInterface(targetIface)
		if err != nil {
			return nil, err
		}
		return cleanIntf.RemoveService(ctx)
	})

	// Redis DEL fallback for IRB artifacts
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "NEWTRON_SERVICE_BINDING|"+targetIface)
		client.Del(c, "VLAN_MEMBER|Vlan100|"+targetIface)
		client.Del(c, "VLAN_INTERFACE|Vlan100|10.1.100.1/24")
		client.Del(c, "VLAN_INTERFACE|Vlan100")
		client.Del(c, "VLAN|Vlan100")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_Vlan100")
		client.Del(c, "SUPPRESS_VLAN_NEIGH|Vlan100")
		client.Del(c, "SAG_GLOBAL|IPv4")
		// Shared VRF artifacts (auto-created by service apply)
		client.Del(c, "VRF|server-vpn")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_server-vpn")
		client.Del(c, "BGP_GLOBALS_AF|server-vpn|l2vpn_evpn")
		client.Del(c, "BGP_EVPN_VNI|server-vpn|10100")
	})

	// Verify: VLAN created
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN", "Vlan100")

	// Verify: VLAN_MEMBER with tagged mode (IRB uses tagged)
	testutil.AssertConfigDBEntry(t, nodeName, "VLAN_MEMBER", "Vlan100|"+targetIface, map[string]string{
		"tagging_mode": "tagged",
	})

	// Verify: VLAN_INTERFACE with VRF binding
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN_INTERFACE", "Vlan100")

	// Verify: anycast gateway IP
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN_INTERFACE", "Vlan100|10.1.100.1/24")

	// Verify: SAG anycast MAC
	testutil.AssertConfigDBEntry(t, nodeName, "SAG_GLOBAL", "IPv4", map[string]string{
		"gwmac": "00:00:00:01:02:03",
	})

	// Verify: service binding
	testutil.AssertConfigDBEntry(t, nodeName, "NEWTRON_SERVICE_BINDING", targetIface, map[string]string{
		"service_name": "server-irb",
	})
}

func TestE2E_RemoveServiceL2(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service-L2", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Redis DEL fallback (registered first, runs last)
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "NEWTRON_SERVICE_BINDING|Ethernet2")
		client.Del(c, "VLAN_MEMBER|Vlan100|Ethernet2")
		client.Del(c, "VLAN|Vlan100")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_Vlan100")
		client.Del(c, "SUPPRESS_VLAN_NEIGH|Vlan100")
	})

	// Apply L2 service
	dev := testutil.LabLockedDevice(t, nodeName)
	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface")
	}

	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; l2-extend requires EVPN")
	}

	applyOp := operations.NewApplyServiceOp(targetIface, "l2-extend", "")
	if err := applyOp.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "l2-extend service not available")
		t.Skipf("l2-extend service not available: %v", err)
	}
	if err := applyOp.Execute(ctx, dev); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Verify service was applied
	testutil.AssertConfigDBEntryExists(t, nodeName, "NEWTRON_SERVICE_BINDING", targetIface)

	// Remove service on fresh locked device
	rmDev := testutil.LabLockedDevice(t, nodeName)
	rmOp := operations.NewRemoveServiceOp(targetIface)
	if err := rmOp.Validate(ctx, rmDev); err != nil {
		t.Fatalf("validate remove: %v", err)
	}
	if err := rmOp.Execute(ctx, rmDev); err != nil {
		t.Fatalf("execute remove: %v", err)
	}

	// Verify: service binding removed
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "NEWTRON_SERVICE_BINDING", targetIface)

	// Verify: VLAN_MEMBER removed
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "VLAN_MEMBER", "Vlan100|"+targetIface)
}

func TestE2E_RemoveServiceIRB(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service-IRB", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Redis DEL fallback (registered first, runs last)
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "NEWTRON_SERVICE_BINDING|Ethernet2")
		client.Del(c, "VLAN_MEMBER|Vlan100|Ethernet2")
		client.Del(c, "VLAN_INTERFACE|Vlan100|10.1.100.1/24")
		client.Del(c, "VLAN_INTERFACE|Vlan100")
		client.Del(c, "VLAN|Vlan100")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_Vlan100")
		client.Del(c, "SUPPRESS_VLAN_NEIGH|Vlan100")
		client.Del(c, "SAG_GLOBAL|IPv4")
		// Shared VRF artifacts
		client.Del(c, "VRF|server-vpn")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_server-vpn")
		client.Del(c, "BGP_GLOBALS_AF|server-vpn|l2vpn_evpn")
		client.Del(c, "BGP_EVPN_VNI|server-vpn|10100")
	})

	// Apply IRB service
	dev := testutil.LabLockedDevice(t, nodeName)
	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface")
	}

	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; server-irb requires EVPN")
	}

	applyOp := operations.NewApplyServiceOp(targetIface, "server-irb", "")
	if err := applyOp.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "server-irb service not available")
		t.Skipf("server-irb service not available: %v", err)
	}
	if err := applyOp.Execute(ctx, dev); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Verify service was applied
	testutil.AssertConfigDBEntryExists(t, nodeName, "NEWTRON_SERVICE_BINDING", targetIface)

	// Remove service on fresh locked device
	rmDev := testutil.LabLockedDevice(t, nodeName)
	rmOp := operations.NewRemoveServiceOp(targetIface)
	if err := rmOp.Validate(ctx, rmDev); err != nil {
		t.Fatalf("validate remove: %v", err)
	}
	if err := rmOp.Execute(ctx, rmDev); err != nil {
		t.Fatalf("execute remove: %v", err)
	}

	// Verify all IRB resources cleaned up
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "NEWTRON_SERVICE_BINDING", targetIface)
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "VLAN_MEMBER", "Vlan100|"+targetIface)
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "VLAN_INTERFACE", "Vlan100|10.1.100.1/24")
}

func TestE2E_ApplyServiceWithFilter(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service-Filter", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface for service with filter")
	}

	// customer-l3 requires ipvpn which needs VTEP for L3 VNI
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; customer-l3 requires EVPN")
	}

	// Apply customer-l3 service (has ingress_filter + egress_filter)
	op := operations.NewApplyServiceOp(targetIface, "customer-l3", "10.99.97.1/30")
	if err := op.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "customer-l3 service not available")
		t.Skipf("customer-l3 service not available: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Cleanup: remove service
	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		cleanIntf, err := d.GetInterface(targetIface)
		if err != nil {
			return nil, err
		}
		return cleanIntf.RemoveService(ctx)
	})

	// Verify service binding
	testutil.AssertConfigDBEntry(t, nodeName, "NEWTRON_SERVICE_BINDING", targetIface, map[string]string{
		"service_name": "customer-l3",
	})

	// Verify ingress ACL table created with the interface bound
	// ACL name is derived by the service layer (e.g., "customer-l3_in")
	verifyDev := testutil.LabConnectedDevice(t, nodeName)
	cdb := verifyDev.ConfigDB()
	foundIngressACL := false
	foundEgressACL := false
	for name, table := range cdb.ACLTable {
		ports := ""
		if table.Ports != "" {
			ports = table.Ports
		}
		if table.Stage == "ingress" && containsPort(ports, targetIface) {
			foundIngressACL = true
			t.Logf("  ingress ACL: %s (ports=%s)", name, ports)
		}
		if table.Stage == "egress" && containsPort(ports, targetIface) {
			foundEgressACL = true
			t.Logf("  egress ACL: %s (ports=%s)", name, ports)
		}
	}

	if !foundIngressACL {
		t.Error("no ingress ACL table found with target interface bound")
	}
	if !foundEgressACL {
		t.Error("no egress ACL table found with target interface bound")
	}

	// Verify at least one ACL rule exists
	foundRule := false
	for key := range cdb.ACLRule {
		t.Logf("  ACL rule: %s", key)
		foundRule = true
	}
	if !foundRule {
		t.Error("no ACL rules found")
	}
}

// containsPort checks if a comma-separated ports string contains the given port.
func containsPort(ports, port string) bool {
	if ports == port {
		return true
	}
	for i := 0; i < len(ports); {
		end := i
		for end < len(ports) && ports[end] != ',' {
			end++
		}
		if ports[i:end] == port {
			return true
		}
		i = end + 1
	}
	return false
}

func TestE2E_SharedVRFMultipleInterfaces(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service-VRF", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	// server-irb requires ipvpn+macvpn which needs VTEP
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; server-irb requires EVPN")
	}

	ports := findFreePhysicalInterfaces(t, dev, 2)
	if len(ports) < 2 {
		testutil.TrackComment(t, "need 2 free interfaces")
		t.Skip("need at least 2 free physical interfaces")
	}

	portA := ports[0]
	portB := ports[1]

	// Redis DEL fallback
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "NEWTRON_SERVICE_BINDING|"+portA)
		client.Del(c, "NEWTRON_SERVICE_BINDING|"+portB)
		client.Del(c, "VLAN_MEMBER|Vlan100|"+portA)
		client.Del(c, "VLAN_MEMBER|Vlan100|"+portB)
		client.Del(c, "VLAN_INTERFACE|Vlan100|10.1.100.1/24")
		client.Del(c, "VLAN_INTERFACE|Vlan100")
		client.Del(c, "VLAN|Vlan100")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_Vlan100")
		client.Del(c, "SUPPRESS_VLAN_NEIGH|Vlan100")
		client.Del(c, "SAG_GLOBAL|IPv4")
		// Shared VRF artifacts
		client.Del(c, "VRF|server-vpn")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10100_server-vpn")
		client.Del(c, "BGP_GLOBALS_AF|server-vpn|l2vpn_evpn")
		client.Del(c, "BGP_EVPN_VNI|server-vpn|10100")
	})

	// server-irb requires VTEP for EVPN
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; server-irb requires EVPN")
	}

	// Apply server-irb to port A
	opA := operations.NewApplyServiceOp(portA, "server-irb", "")
	if err := opA.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "server-irb service not available")
		t.Skipf("server-irb not available: %v", err)
	}
	if err := opA.Execute(ctx, dev); err != nil {
		t.Fatalf("apply to %s: %v", portA, err)
	}

	// Verify VLAN exists after first apply
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN", "Vlan100")

	// Apply server-irb to port B on fresh locked device
	devB := testutil.LabLockedDevice(t, nodeName)
	opB := operations.NewApplyServiceOp(portB, "server-irb", "")
	if err := opB.Validate(ctx, devB); err != nil {
		t.Fatalf("validate apply to %s: %v", portB, err)
	}
	if err := opB.Execute(ctx, devB); err != nil {
		t.Fatalf("apply to %s: %v", portB, err)
	}

	// Verify both bindings exist
	testutil.AssertConfigDBEntryExists(t, nodeName, "NEWTRON_SERVICE_BINDING", portA)
	testutil.AssertConfigDBEntryExists(t, nodeName, "NEWTRON_SERVICE_BINDING", portB)

	// Remove from port A — VLAN should still exist (not last user)
	rmDevA := testutil.LabLockedDevice(t, nodeName)
	rmOpA := operations.NewRemoveServiceOp(portA)
	if err := rmOpA.Validate(ctx, rmDevA); err != nil {
		t.Fatalf("validate remove from %s: %v", portA, err)
	}
	if err := rmOpA.Execute(ctx, rmDevA); err != nil {
		t.Fatalf("remove from %s: %v", portA, err)
	}

	testutil.AssertConfigDBEntryAbsent(t, nodeName, "NEWTRON_SERVICE_BINDING", portA)
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN", "Vlan100")

	// Remove from port B — VLAN should be deleted (last user)
	rmDevB := testutil.LabLockedDevice(t, nodeName)
	rmOpB := operations.NewRemoveServiceOp(portB)
	if err := rmOpB.Validate(ctx, rmDevB); err != nil {
		t.Fatalf("validate remove from %s: %v", portB, err)
	}
	if err := rmOpB.Execute(ctx, rmDevB); err != nil {
		t.Fatalf("remove from %s: %v", portB, err)
	}

	testutil.AssertConfigDBEntryAbsent(t, nodeName, "NEWTRON_SERVICE_BINDING", portB)
}

func TestE2E_RemoveServiceDependencyCheck(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service-Deps", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	// customer-l3 requires ipvpn which needs VTEP for L3 VNI
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; customer-l3 requires EVPN")
	}

	ports := findFreePhysicalInterfaces(t, dev, 2)
	if len(ports) < 2 {
		testutil.TrackComment(t, "need 2 free interfaces")
		t.Skip("need at least 2 free physical interfaces")
	}

	portA := ports[0]
	portB := ports[1]

	// Redis DEL fallback
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "NEWTRON_SERVICE_BINDING|"+portA)
		client.Del(c, "NEWTRON_SERVICE_BINDING|"+portB)
		// customer-l3 creates per-interface VRFs and ACLs — clean up generically
		keys, _ := client.Keys(c, "ACL_TABLE|customer-l3*").Result()
		for _, k := range keys {
			client.Del(c, k)
		}
		keys, _ = client.Keys(c, "ACL_RULE|customer-l3*").Result()
		for _, k := range keys {
			client.Del(c, k)
		}
		keys, _ = client.Keys(c, "VRF|Vrf_*").Result()
		for _, k := range keys {
			client.Del(c, k)
		}
		keys, _ = client.Keys(c, "INTERFACE|"+portA+"*").Result()
		for _, k := range keys {
			client.Del(c, k)
		}
		keys, _ = client.Keys(c, "INTERFACE|"+portB+"*").Result()
		for _, k := range keys {
			client.Del(c, k)
		}
	})

	// customer-l3 requires ipvpn which needs VTEP for L3 VNI
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; customer-l3 requires EVPN")
	}

	// Apply customer-l3 to port A
	opA := operations.NewApplyServiceOp(portA, "customer-l3", "10.99.96.1/30")
	if err := opA.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "customer-l3 service not available")
		t.Skipf("customer-l3 not available: %v", err)
	}
	if err := opA.Execute(ctx, dev); err != nil {
		t.Fatalf("apply to %s: %v", portA, err)
	}

	// Apply customer-l3 to port B on fresh locked device
	devB := testutil.LabLockedDevice(t, nodeName)
	opB := operations.NewApplyServiceOp(portB, "customer-l3", "10.99.95.1/30")
	if err := opB.Validate(ctx, devB); err != nil {
		t.Fatalf("validate apply to %s: %v", portB, err)
	}
	if err := opB.Execute(ctx, devB); err != nil {
		t.Fatalf("apply to %s: %v", portB, err)
	}

	// Both should be bound
	testutil.AssertConfigDBEntryExists(t, nodeName, "NEWTRON_SERVICE_BINDING", portA)
	testutil.AssertConfigDBEntryExists(t, nodeName, "NEWTRON_SERVICE_BINDING", portB)

	// Remove from port A
	rmDevA := testutil.LabLockedDevice(t, nodeName)
	rmOpA := operations.NewRemoveServiceOp(portA)
	if err := rmOpA.Validate(ctx, rmDevA); err != nil {
		t.Fatalf("validate remove from %s: %v", portA, err)
	}
	if err := rmOpA.Execute(ctx, rmDevA); err != nil {
		t.Fatalf("remove from %s: %v", portA, err)
	}

	// Port A binding gone, port B binding still exists
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "NEWTRON_SERVICE_BINDING", portA)
	testutil.AssertConfigDBEntryExists(t, nodeName, "NEWTRON_SERVICE_BINDING", portB)

	// Remove from port B
	rmDevB := testutil.LabLockedDevice(t, nodeName)
	rmOpB := operations.NewRemoveServiceOp(portB)
	if err := rmOpB.Validate(ctx, rmDevB); err != nil {
		t.Fatalf("validate remove from %s: %v", portB, err)
	}
	if err := rmOpB.Execute(ctx, rmDevB); err != nil {
		t.Fatalf("remove from %s: %v", portB, err)
	}

	testutil.AssertConfigDBEntryAbsent(t, nodeName, "NEWTRON_SERVICE_BINDING", portB)
}
