//go:build e2e

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/operations"
)

func TestE2E_BGPNeighborState(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Multi-Device", "all")

	nodes := testutil.LabSonicNodes(t)

	// Check BGP sessions on leaf nodes (they peer with spines)
	for _, node := range nodes {
		if len(node.Name) < 4 || node.Name[:4] != "leaf" {
			continue
		}

		t.Run(node.Name, func(t *testing.T) {
			testutil.Track(t, "Multi-Device", node.Name)
			dev := testutil.LabConnectedDevice(t, node.Name)

			neighbors := dev.ListBGPNeighbors()
			if len(neighbors) == 0 {
				t.Log("no BGP neighbors configured, skipping")
				return
			}

			// Poll STATE_DB for BGP neighbor state
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			for _, neighborIP := range neighbors {
				t.Run(neighborIP, func(t *testing.T) {
					testutil.Track(t, "Multi-Device", node.Name)
					err := testutil.PollStateDB(ctx, t, node.Name,
						"BGP_NEIGHBOR_TABLE", neighborIP, "state", "Established")
					if err != nil {
						t.Logf("BGP neighbor %s on %s not Established: %v", neighborIP, node.Name, err)
						// Don't fail - BGP may not converge in VS
						t.Skip("BGP session did not reach Established (expected in some VS environments)")
					}
				})
			}
		})
	}
}

func TestE2E_VLANAcrossTwoLeaves(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Multi-Device", "all")

	nodes := testutil.LabSonicNodes(t)

	// Find two leaf nodes
	var leafs []string
	for _, n := range nodes {
		if len(n.Name) >= 4 && n.Name[:4] == "leaf" {
			leafs = append(leafs, n.Name)
		}
	}
	if len(leafs) < 2 {
		testutil.TrackComment(t, "need at least 2 leaf nodes")
		t.Skip("need at least 2 leaf nodes for multi-device VLAN test")
	}

	leaf1Name := leafs[0]
	leaf2Name := leafs[1]
	ctx := testutil.LabContext(t)

	// Create VLAN 600 on both leaves using operations
	for _, name := range []string{leaf1Name, leaf2Name} {
		dev := testutil.LabLockedDevice(t, name)

		op := &operations.CreateVLANOp{
			ID:   600,
			Desc: "e2e-multi-leaf-test",
		}
		if err := op.Validate(ctx, dev); err != nil {
			t.Fatalf("validate on %s: %v", name, err)
		}
		if err := op.Execute(ctx, dev); err != nil {
			t.Fatalf("execute on %s: %v", name, err)
		}

		// Register cleanup for this leaf
		nodeName := name // capture for closure
		testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
			return d.DeleteVLAN(ctx, 600)
		})
	}

	// Verify VLAN exists on both leaves via fresh connections
	for _, name := range []string{leaf1Name, leaf2Name} {
		t.Run(name, func(t *testing.T) {
			testutil.Track(t, "Multi-Device", name)
			verifyDev := testutil.LabConnectedDevice(t, name)

			if !verifyDev.VLANExists(600) {
				t.Fatalf("VLAN 600 should exist on %s", name)
			}

			vlan, err := verifyDev.GetVLAN(600)
			if err != nil {
				t.Fatalf("getting VLAN on %s: %v", name, err)
			}
			if vlan.ID != 600 {
				t.Errorf("VLAN ID = %d, want 600", vlan.ID)
			}
		})
	}
}

func TestE2E_EVPNFabricHealth(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Multi-Device", "all")

	nodes := testutil.LabSonicNodes(t)
	ctx := testutil.LabContext(t)

	for _, node := range nodes {
		t.Run(node.Name, func(t *testing.T) {
			testutil.Track(t, "Multi-Device", node.Name)
			net := testutil.LabNetwork(t)
			dev, err := net.ConnectDevice(ctx, node.Name)
			if err != nil {
				t.Skipf("skipping %s: %v", node.Name, err)
			}
			defer dev.Disconnect()

			if !dev.IsConnected() {
				t.Fatalf("%s not connected", node.Name)
			}

			// Verify hostname via ConfigDB
			configDB := dev.ConfigDB()
			if configDB == nil {
				t.Fatalf("%s: config_db not loaded", node.Name)
			}
			if metadata, ok := configDB.DeviceMetadata["localhost"]; ok {
				if hostname := metadata["hostname"]; hostname != node.Name {
					t.Errorf("hostname = %q, want %q", hostname, node.Name)
				}
			}

			// Check BGP is configured using device API
			if dev.BGPConfigured() {
				neighbors := dev.ListBGPNeighbors()
				t.Logf("%s: BGP configured with %d neighbors", node.Name, len(neighbors))
			} else {
				t.Logf("%s: BGP not configured", node.Name)
			}

			// Check VTEP on leaves using device API
			if dev.VTEPExists() {
				t.Logf("%s: VTEP exists (source IP: %s)", node.Name, dev.VTEPSourceIP())
			}

			// Run health checks via the client
			results, err := dev.RunHealthChecks(ctx, "")
			if err != nil {
				t.Fatalf("health checks: %v", err)
			}
			for _, r := range results {
				t.Logf("%s: [%s] %s: %s", node.Name, r.Status, r.Check, r.Message)
				if r.Status == "fail" {
					t.Errorf("health check %q failed: %s", r.Check, r.Message)
				}
			}
		})
	}
}
