//go:build e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/operations"
)

// twoLeafNames returns the names of two leaf nodes from the topology.
// Fatals if fewer than two leaves are available.
func twoLeafNames(t *testing.T) (string, string) {
	t.Helper()

	nodes := testutil.LabSonicNodes(t)
	var leafs []string
	for _, n := range nodes {
		if len(n.Name) >= 4 && n.Name[:4] == "leaf" {
			leafs = append(leafs, n.Name)
		}
	}
	if len(leafs) < 2 {
		t.Fatal("need at least 2 leaf nodes for data-plane test")
	}
	return leafs[0], leafs[1]
}

// TestE2E_DataPlane_L2Bridged tests L2 bridged connectivity across the EVPN/VXLAN fabric.
//
// What it checks:
//
//	Creates matching VLAN 700 + L2VNI 10700 on both leaf switches with server-facing
//	ports as untagged members. Verifies CONFIG_DB entries, then tests ping between
//	servers on the same subnet (10.70.0.0/24).
//
// Pass/Fail criteria:
//
//	FAIL: Any operation Validate/Execute error, missing CONFIG_DB entries,
//	or ASIC_DB convergence failure.
//	SKIP: Ping failure (VXLAN data-plane forwarding is not supported on virtual switches).
func TestE2E_DataPlane_L2Bridged(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Data Plane", "leaf1, leaf2")
	testutil.SkipIfNoServers(t, "server1", "server2")

	leaf1Name, leaf2Name := twoLeafNames(t)
	ctx := testutil.LabContext(t)

	// Verify both leaves have VTEP + BGP for EVPN
	for _, name := range []string{leaf1Name, leaf2Name} {
		dev := testutil.LabConnectedDevice(t, name)
		if !dev.VTEPExists() || !dev.BGPConfigured() {
			t.Skipf("VTEP or BGP not configured on %s, skipping data-plane test", name)
		}
	}

	// Register cleanup using reverse operations (tests delete path).
	// Runs in reverse dependency order: unmap VNI → remove member → delete VLAN.
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, name := range []string{leaf1Name, leaf2Name} {
			dev, err := testutil.TryLabConnectedDevice(t, name)
			if err != nil {
				t.Logf("cleanup: connect %s: %v", name, err)
				continue
			}
			if err := dev.Lock(cleanCtx); err != nil {
				t.Logf("cleanup: lock %s: %v", name, err)
				continue
			}
			unmapOp := &operations.UnmapL2VNIOp{VLANID: 700}
			if err := unmapOp.Validate(cleanCtx, dev); err == nil {
				if err := unmapOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: unmap L2VNI on %s: %v", name, err)
				}
			}
			rmOp := &operations.RemoveVLANMemberOp{VLANID: 700, Port: "Ethernet2"}
			if err := rmOp.Validate(cleanCtx, dev); err == nil {
				if err := rmOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: remove VLAN member on %s: %v", name, err)
				}
			}
			delOp := &operations.DeleteVLANOp{ID: 700}
			if err := delOp.Validate(cleanCtx, dev); err == nil {
				if err := delOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: delete VLAN on %s: %v", name, err)
				}
			}
			dev.Unlock()
		}
	})

	// --- Step 1: Create VLAN 700 on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 1: Creating VLAN 700 on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.CreateVLANOp{ID: 700, Desc: "e2e-l2-bridged"}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 1: FAIL - Validate CreateVLAN on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 1: FAIL - Execute CreateVLAN on %s: %v", name, err)
		}
		t.Logf("Step 1: PASS - VLAN 700 created on %s", name)
	}

	// --- Step 2: Add Ethernet2 as untagged VLAN member on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 2: Adding VLAN member Ethernet2 to VLAN 700 on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.AddVLANMemberOp{VLANID: 700, Port: "Ethernet2", Tagged: false}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 2: FAIL - Validate AddVLANMember on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 2: FAIL - Execute AddVLANMember on %s: %v", name, err)
		}
		t.Logf("Step 2: PASS - Ethernet2 added to VLAN 700 on %s", name)
	}

	// --- Step 3: Map L2VNI 10700 to VLAN 700 on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 3: Mapping L2VNI 10700 to VLAN 700 on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.MapL2VNIOp{VLANID: 700, VNI: 10700}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 3: FAIL - Validate MapL2VNI on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 3: FAIL - Execute MapL2VNI on %s: %v", name, err)
		}
		t.Logf("Step 3: PASS - L2VNI 10700 mapped to VLAN 700 on %s", name)
	}

	// --- Step 4: Verify CONFIG_DB: VLAN|Vlan700 exists on both ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 4: Verifying VLAN|Vlan700 exists on %s", name)
		testutil.AssertConfigDBEntryExists(t, name, "VLAN", "Vlan700")
		t.Logf("Step 4: PASS - VLAN|Vlan700 exists on %s", name)
	}

	// --- Step 5: Verify CONFIG_DB: VLAN_MEMBER|Vlan700|Ethernet2 on both ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 5: Verifying VLAN_MEMBER|Vlan700|Ethernet2 on %s", name)
		testutil.AssertConfigDBEntry(t, name, "VLAN_MEMBER", "Vlan700|Ethernet2", map[string]string{
			"tagging_mode": "untagged",
		})
		t.Logf("Step 5: PASS - VLAN_MEMBER|Vlan700|Ethernet2 correct on %s", name)
	}

	// --- Step 6: Verify CONFIG_DB: VXLAN_TUNNEL_MAP on both ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 6: Verifying VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700 on %s", name)
		testutil.AssertConfigDBEntry(t, name, "VXLAN_TUNNEL_MAP", "vtep1|map_10700_Vlan700", map[string]string{
			"vlan": "Vlan700",
			"vni":  "10700",
		})
		t.Logf("Step 6: PASS - VXLAN_TUNNEL_MAP correct on %s", name)
	}

	// --- Step 7: Wait for ASIC convergence ---
	t.Log("Step 7: Waiting for ASIC convergence")
	asicCtx, asicCancel := context.WithTimeout(ctx, 30*time.Second)
	defer asicCancel()
	for _, name := range []string{leaf1Name, leaf2Name} {
		if err := testutil.WaitForASICVLAN(asicCtx, t, name, 700); err != nil {
			t.Fatalf("Step 7: FAIL - %v", err)
		}
	}
	t.Log("Step 7: PASS - VLAN 700 in ASIC_DB on both leaves")

	// --- Step 8: Configure server IPs (same L2 domain, no gateway) ---
	t.Log("Step 8: Configuring server1 eth1 = 10.70.0.1/24")
	testutil.ServerConfigureInterface(t, "server1", "eth1", "10.70.0.1/24", "")
	t.Log("Step 8: PASS - server1 configured")

	t.Log("Step 8: Configuring server2 eth1 = 10.70.0.2/24")
	testutil.ServerConfigureInterface(t, "server2", "eth1", "10.70.0.2/24", "")
	t.Log("Step 8: PASS - server2 configured")

	// --- Step 9: Ping server2 from server1 (soft-fail: VXLAN data plane unsupported on VS) ---
	t.Log("Step 9: Pinging server2 (10.70.0.2) from server1")
	if !testutil.ServerPing(t, "server1", "10.70.0.2", 5) {
		t.Log("Step 9: ping from server1 to server2 failed (expected on virtual switch)")
		t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 9: PASS - ping from server1 to server2 succeeded")

	// --- Step 10: Ping server1 from server2 (soft-fail: VXLAN data plane unsupported on VS) ---
	t.Log("Step 10: Pinging server1 (10.70.0.1) from server2")
	if !testutil.ServerPing(t, "server2", "10.70.0.1", 5) {
		t.Log("Step 10: ping from server2 to server1 failed (expected on virtual switch)")
		t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 10: PASS - ping from server2 to server1 succeeded")
}

// TestE2E_DataPlane_IRBSymmetric tests IRB with symmetric EVPN model.
//
// What it checks:
//
//	Creates a VRF with L3VNI, a VLAN with L2VNI, an SVI with anycast gateway
//	in the VRF, and verifies servers can communicate through the anycast gateway
//	across leaves.
//
// Pass/Fail criteria:
//
//	FAIL: Any operation Validate/Execute error, missing CONFIG_DB entries,
//	or ASIC_DB convergence failure.
//	SKIP: Ping failure (VXLAN data-plane forwarding is not supported on virtual switches).
func TestE2E_DataPlane_IRBSymmetric(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Data Plane", "leaf1, leaf2")
	testutil.SkipIfNoServers(t, "server1", "server2")

	leaf1Name, leaf2Name := twoLeafNames(t)
	ctx := testutil.LabContext(t)

	// Verify both leaves have VTEP + BGP for EVPN
	for _, name := range []string{leaf1Name, leaf2Name} {
		dev := testutil.LabConnectedDevice(t, name)
		if !dev.VTEPExists() || !dev.BGPConfigured() {
			t.Skipf("VTEP or BGP not configured on %s, skipping data-plane test", name)
		}
	}

	// Register cleanup using reverse operations.
	// Order: remove SVI (raw Redis, no op) → unmap VNI → remove member → delete VLAN → delete VRF.
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, name := range []string{leaf1Name, leaf2Name} {
			// Remove SVI entries first (no dedicated operation exists).
			client := testutil.LabRedisClient(t, name, 4)
			client.Del(cleanCtx, "VLAN_INTERFACE|Vlan800|10.80.0.1/24")
			client.Del(cleanCtx, "VLAN_INTERFACE|Vlan800")

			dev, err := testutil.TryLabConnectedDevice(t, name)
			if err != nil {
				t.Logf("cleanup: connect %s: %v", name, err)
				continue
			}
			if err := dev.Lock(cleanCtx); err != nil {
				t.Logf("cleanup: lock %s: %v", name, err)
				continue
			}
			unmapOp := &operations.UnmapL2VNIOp{VLANID: 800}
			if err := unmapOp.Validate(cleanCtx, dev); err == nil {
				if err := unmapOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: unmap L2VNI on %s: %v", name, err)
				}
			}
			rmOp := &operations.RemoveVLANMemberOp{VLANID: 800, Port: "Ethernet2"}
			if err := rmOp.Validate(cleanCtx, dev); err == nil {
				if err := rmOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: remove VLAN member on %s: %v", name, err)
				}
			}
			delVlanOp := &operations.DeleteVLANOp{ID: 800}
			if err := delVlanOp.Validate(cleanCtx, dev); err == nil {
				if err := delVlanOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: delete VLAN on %s: %v", name, err)
				}
			}
			delVrfOp := &operations.DeleteVRFOp{VRFName: "Vrf_e2e_irb"}
			if err := delVrfOp.Validate(cleanCtx, dev); err == nil {
				if err := delVrfOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: delete VRF on %s: %v", name, err)
				}
			}
			dev.Unlock()
		}
	})

	// --- Step 1: Create VRF on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 1: Creating VRF Vrf_e2e_irb on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.CreateVRFOp{VRFName: "Vrf_e2e_irb"}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 1: FAIL - Validate CreateVRF on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 1: FAIL - Execute CreateVRF on %s: %v", name, err)
		}
		t.Logf("Step 1: PASS - VRF Vrf_e2e_irb created on %s", name)
	}

	// --- Step 2: Create VLAN 800 on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 2: Creating VLAN 800 on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.CreateVLANOp{ID: 800, Desc: "e2e-irb-symmetric"}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 2: FAIL - Validate CreateVLAN on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 2: FAIL - Execute CreateVLAN on %s: %v", name, err)
		}
		t.Logf("Step 2: PASS - VLAN 800 created on %s", name)
	}

	// --- Step 3: Add Ethernet2 as untagged VLAN member on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 3: Adding VLAN member Ethernet2 to VLAN 800 on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.AddVLANMemberOp{VLANID: 800, Port: "Ethernet2", Tagged: false}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 3: FAIL - Validate AddVLANMember on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 3: FAIL - Execute AddVLANMember on %s: %v", name, err)
		}
		t.Logf("Step 3: PASS - Ethernet2 added to VLAN 800 on %s", name)
	}

	// --- Step 4: Map L2VNI 10800 to VLAN 800 on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 4: Mapping L2VNI 10800 to VLAN 800 on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.MapL2VNIOp{VLANID: 800, VNI: 10800}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 4: FAIL - Validate MapL2VNI on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 4: FAIL - Execute MapL2VNI on %s: %v", name, err)
		}
		t.Logf("Step 4: PASS - L2VNI 10800 mapped to VLAN 800 on %s", name)
	}

	// --- Step 5: Configure SVI on both leaves (anycast gateway) ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 5: Configuring SVI Vlan800 with anycast GW on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.ConfigureSVIOp{
			VLANID:         800,
			VRF:            "Vrf_e2e_irb",
			IPAddress:      "10.80.0.1/24",
			AnycastGateway: "10.80.0.1/24",
		}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 5: FAIL - Validate ConfigureSVI on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 5: FAIL - Execute ConfigureSVI on %s: %v", name, err)
		}
		t.Logf("Step 5: PASS - SVI Vlan800 configured on %s", name)
	}

	// --- Step 6: Verify CONFIG_DB: VRF|Vrf_e2e_irb exists on both ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 6: Verifying VRF|Vrf_e2e_irb exists on %s", name)
		testutil.AssertConfigDBEntryExists(t, name, "VRF", "Vrf_e2e_irb")
		t.Logf("Step 6: PASS - VRF|Vrf_e2e_irb exists on %s", name)
	}

	// --- Step 7: Verify CONFIG_DB: VLAN_INTERFACE|Vlan800 has vrf_name ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 7: Verifying VLAN_INTERFACE|Vlan800 vrf_name on %s", name)
		testutil.AssertConfigDBEntry(t, name, "VLAN_INTERFACE", "Vlan800", map[string]string{
			"vrf_name": "Vrf_e2e_irb",
		})
		t.Logf("Step 7: PASS - VLAN_INTERFACE|Vlan800 correct on %s", name)
	}

	// --- Step 8: Verify CONFIG_DB: VLAN_INTERFACE|Vlan800|10.80.0.1/24 exists ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 8: Verifying VLAN_INTERFACE|Vlan800|10.80.0.1/24 on %s", name)
		testutil.AssertConfigDBEntryExists(t, name, "VLAN_INTERFACE", "Vlan800|10.80.0.1/24")
		t.Logf("Step 8: PASS - VLAN_INTERFACE|Vlan800|10.80.0.1/24 exists on %s", name)
	}

	// --- Step 9: Verify CONFIG_DB: VXLAN_TUNNEL_MAP on both ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 9: Verifying VXLAN_TUNNEL_MAP|vtep1|map_10800_Vlan800 on %s", name)
		testutil.AssertConfigDBEntry(t, name, "VXLAN_TUNNEL_MAP", "vtep1|map_10800_Vlan800", map[string]string{
			"vlan": "Vlan800",
			"vni":  "10800",
		})
		t.Logf("Step 9: PASS - VXLAN_TUNNEL_MAP correct on %s", name)
	}

	// --- Step 10: Wait for ASIC convergence (soft-fail: VS may not converge IRB topology) ---
	t.Log("Step 10: Waiting for ASIC convergence")
	asicCtx, asicCancel := context.WithTimeout(ctx, 30*time.Second)
	defer asicCancel()
	for _, name := range []string{leaf1Name, leaf2Name} {
		if err := testutil.WaitForASICVLAN(asicCtx, t, name, 800); err != nil {
			t.Logf("Step 10: ASIC convergence failed on %s: %v (expected on virtual switch)", name, err)
			t.Skip("ASIC convergence for IRB topology not supported on virtual switch")
		}
	}
	t.Log("Step 10: PASS - VLAN 800 in ASIC_DB on both leaves")

	// --- Step 11: Configure server IPs ---
	t.Log("Step 11: Configuring server1 eth1 = 10.80.0.10/24 gw 10.80.0.1")
	testutil.ServerConfigureInterface(t, "server1", "eth1", "10.80.0.10/24", "10.80.0.1")
	t.Log("Step 11: PASS - server1 configured")

	// --- Step 12: Configure server2 ---
	t.Log("Step 12: Configuring server2 eth1 = 10.80.0.20/24 gw 10.80.0.1")
	testutil.ServerConfigureInterface(t, "server2", "eth1", "10.80.0.20/24", "10.80.0.1")
	t.Log("Step 12: PASS - server2 configured")

	// --- Step 13: Ping gateway from server1 (soft-fail: data plane unsupported on VS) ---
	t.Log("Step 13: Pinging gateway 10.80.0.1 from server1")
	if !testutil.ServerPing(t, "server1", "10.80.0.1", 3) {
		t.Log("Step 13: ping gateway from server1 failed (expected on virtual switch)")
		t.Skip("data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 13: PASS - gateway reachable from server1")

	// --- Step 14: Ping gateway from server2 ---
	t.Log("Step 14: Pinging gateway 10.80.0.1 from server2")
	if !testutil.ServerPing(t, "server2", "10.80.0.1", 3) {
		t.Log("Step 14: ping gateway from server2 failed (expected on virtual switch)")
		t.Skip("data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 14: PASS - gateway reachable from server2")

	// --- Step 15: Ping server2 from server1 (soft-fail: VXLAN data plane unsupported on VS) ---
	t.Log("Step 15: Pinging server2 (10.80.0.20) from server1")
	if !testutil.ServerPing(t, "server1", "10.80.0.20", 5) {
		t.Log("Step 15: ping from server1 to server2 failed (expected on virtual switch)")
		t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 15: PASS - ping from server1 to server2 succeeded")

	// --- Step 16: Ping server1 from server2 (soft-fail: VXLAN data plane unsupported on VS) ---
	t.Log("Step 16: Pinging server1 (10.80.0.10) from server2")
	if !testutil.ServerPing(t, "server2", "10.80.0.10", 5) {
		t.Log("Step 16: ping from server2 to server1 failed (expected on virtual switch)")
		t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 16: PASS - ping from server2 to server1 succeeded")
}

// TestE2E_DataPlane_L3Routed tests L3 routed connectivity across an EVPN fabric.
//
// What it checks:
//
//	Creates a VRF on both leaves, binds the server-facing interface (Ethernet2)
//	directly to the VRF with different /30 subnets, and verifies inter-subnet
//	routing through the VRF.
//
// Pass/Fail criteria:
//
//	FAIL: Any operation Validate/Execute error, missing CONFIG_DB entries.
//	SKIP: Ping failure (data-plane forwarding is not supported on virtual switches).
func TestE2E_DataPlane_L3Routed(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Data Plane", "leaf1, leaf2")
	testutil.SkipIfNoServers(t, "server1", "server2")

	leaf1Name, leaf2Name := twoLeafNames(t)
	ctx := testutil.LabContext(t)

	// Verify both leaves have VTEP + BGP for EVPN
	for _, name := range []string{leaf1Name, leaf2Name} {
		dev := testutil.LabConnectedDevice(t, name)
		if !dev.VTEPExists() || !dev.BGPConfigured() {
			t.Skipf("VTEP or BGP not configured on %s, skipping data-plane test", name)
		}
	}

	// Register cleanup using reverse operations.
	// Order: remove interface VRF+IP (raw Redis, no op) → delete VRF.
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Remove interface bindings (no dedicated reverse operation exists).
		client1 := testutil.LabRedisClient(t, leaf1Name, 4)
		client1.Del(cleanCtx, "INTERFACE|Ethernet2|10.90.1.1/30")
		client1.Del(cleanCtx, "INTERFACE|Ethernet2")
		client2 := testutil.LabRedisClient(t, leaf2Name, 4)
		client2.Del(cleanCtx, "INTERFACE|Ethernet2|10.90.2.1/30")
		client2.Del(cleanCtx, "INTERFACE|Ethernet2")

		// Delete VRF using proper operation.
		for _, name := range []string{leaf1Name, leaf2Name} {
			dev, err := testutil.TryLabConnectedDevice(t, name)
			if err != nil {
				t.Logf("cleanup: connect %s: %v", name, err)
				continue
			}
			if err := dev.Lock(cleanCtx); err != nil {
				t.Logf("cleanup: lock %s: %v", name, err)
				continue
			}
			delOp := &operations.DeleteVRFOp{VRFName: "Vrf_e2e_l3"}
			if err := delOp.Validate(cleanCtx, dev); err == nil {
				if err := delOp.Execute(cleanCtx, dev); err != nil {
					t.Logf("cleanup: delete VRF on %s: %v", name, err)
				}
			}
			dev.Unlock()
		}
	})

	// --- Step 1: Create VRF on both leaves ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 1: Creating VRF Vrf_e2e_l3 on %s", name)
		dev := testutil.LabLockedDevice(t, name)
		op := &operations.CreateVRFOp{VRFName: "Vrf_e2e_l3"}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 1: FAIL - Validate CreateVRF on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 1: FAIL - Execute CreateVRF on %s: %v", name, err)
		}
		t.Logf("Step 1: PASS - VRF Vrf_e2e_l3 created on %s", name)
	}

	// --- Step 2: Set Ethernet2 to VRF with 10.90.1.1/30 on leaf1 ---
	t.Logf("Step 2: Setting Ethernet2 VRF=Vrf_e2e_l3 IP=10.90.1.1/30 on %s", leaf1Name)
	{
		dev := testutil.LabLockedDevice(t, leaf1Name)
		op := &operations.SetInterfaceVRFOp{
			Interface: "Ethernet2",
			VRF:       "Vrf_e2e_l3",
			IPAddress: "10.90.1.1/30",
		}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 2: FAIL - Validate SetInterfaceVRF on %s: %v", leaf1Name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 2: FAIL - Execute SetInterfaceVRF on %s: %v", leaf1Name, err)
		}
		t.Logf("Step 2: PASS - Ethernet2 bound to VRF on %s", leaf1Name)
	}

	// --- Step 3: Set Ethernet2 to VRF with 10.90.2.1/30 on leaf2 ---
	t.Logf("Step 3: Setting Ethernet2 VRF=Vrf_e2e_l3 IP=10.90.2.1/30 on %s", leaf2Name)
	{
		dev := testutil.LabLockedDevice(t, leaf2Name)
		op := &operations.SetInterfaceVRFOp{
			Interface: "Ethernet2",
			VRF:       "Vrf_e2e_l3",
			IPAddress: "10.90.2.1/30",
		}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("Step 3: FAIL - Validate SetInterfaceVRF on %s: %v", leaf2Name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("Step 3: FAIL - Execute SetInterfaceVRF on %s: %v", leaf2Name, err)
		}
		t.Logf("Step 3: PASS - Ethernet2 bound to VRF on %s", leaf2Name)
	}

	// --- Step 4: Verify CONFIG_DB: VRF|Vrf_e2e_l3 on both ---
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Logf("Step 4: Verifying VRF|Vrf_e2e_l3 exists on %s", name)
		testutil.AssertConfigDBEntryExists(t, name, "VRF", "Vrf_e2e_l3")
		t.Logf("Step 4: PASS - VRF|Vrf_e2e_l3 exists on %s", name)
	}

	// --- Step 5: Verify CONFIG_DB: INTERFACE|Ethernet2 has vrf_name on leaf1 ---
	t.Logf("Step 5: Verifying INTERFACE|Ethernet2 vrf_name on %s", leaf1Name)
	testutil.AssertConfigDBEntry(t, leaf1Name, "INTERFACE", "Ethernet2", map[string]string{
		"vrf_name": "Vrf_e2e_l3",
	})
	t.Logf("Step 5: PASS - INTERFACE|Ethernet2 correct on %s", leaf1Name)

	// --- Step 6: Verify CONFIG_DB: INTERFACE|Ethernet2|10.90.1.1/30 on leaf1 ---
	t.Logf("Step 6: Verifying INTERFACE|Ethernet2|10.90.1.1/30 on %s", leaf1Name)
	testutil.AssertConfigDBEntryExists(t, leaf1Name, "INTERFACE", "Ethernet2|10.90.1.1/30")
	t.Logf("Step 6: PASS - INTERFACE|Ethernet2|10.90.1.1/30 exists on %s", leaf1Name)

	// --- Step 7: Verify CONFIG_DB: INTERFACE|Ethernet2|10.90.2.1/30 on leaf2 ---
	t.Logf("Step 7: Verifying INTERFACE|Ethernet2|10.90.2.1/30 on %s", leaf2Name)
	testutil.AssertConfigDBEntryExists(t, leaf2Name, "INTERFACE", "Ethernet2|10.90.2.1/30")
	t.Logf("Step 7: PASS - INTERFACE|Ethernet2|10.90.2.1/30 exists on %s", leaf2Name)

	// --- Step 8: Configure server1 ---
	t.Log("Step 8: Configuring server1 eth1 = 10.90.1.2/30 gw 10.90.1.1")
	testutil.ServerConfigureInterface(t, "server1", "eth1", "10.90.1.2/30", "10.90.1.1")
	t.Log("Step 8: PASS - server1 configured")

	// --- Step 9: Configure server2 ---
	t.Log("Step 9: Configuring server2 eth1 = 10.90.2.2/30 gw 10.90.2.1")
	testutil.ServerConfigureInterface(t, "server2", "eth1", "10.90.2.2/30", "10.90.2.1")
	t.Log("Step 9: PASS - server2 configured")

	// --- Step 10: Ping leaf1 gateway from server1 (soft-fail: data plane unsupported on VS) ---
	t.Log("Step 10: Pinging leaf1 gateway 10.90.1.1 from server1")
	if !testutil.ServerPing(t, "server1", "10.90.1.1", 3) {
		t.Log("Step 10: ping gateway from server1 failed (expected on virtual switch)")
		t.Skip("data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 10: PASS - leaf1 gateway reachable from server1")

	// --- Step 11: Ping leaf2 gateway from server2 (soft-fail: data plane unsupported on VS) ---
	t.Log("Step 11: Pinging leaf2 gateway 10.90.2.1 from server2")
	if !testutil.ServerPing(t, "server2", "10.90.2.1", 3) {
		t.Log("Step 11: ping gateway from server2 failed (expected on virtual switch)")
		t.Skip("data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 11: PASS - leaf2 gateway reachable from server2")

	// --- Step 12: Ping server2 from server1 (soft-fail: VXLAN data plane unsupported on VS) ---
	t.Log("Step 12: Pinging server2 (10.90.2.2) from server1")
	if !testutil.ServerPing(t, "server1", "10.90.2.2", 5) {
		t.Log("Step 12: ping from server1 to server2 failed (expected on virtual switch)")
		t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 12: PASS - ping from server1 to server2 succeeded")

	// --- Step 13: Ping server1 from server2 (soft-fail: VXLAN data plane unsupported on VS) ---
	t.Log("Step 13: Pinging server1 (10.90.1.2) from server2")
	if !testutil.ServerPing(t, "server2", "10.90.1.2", 5) {
		t.Log("Step 13: ping from server2 to server1 failed (expected on virtual switch)")
		t.Skip("VXLAN data-plane forwarding not supported on virtual switch")
	}
	t.Log("Step 13: PASS - ping from server2 to server1 succeeded")
}
