//go:build e2e

package e2e_test

import (
	"os"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/network"
)

func TestE2E_ValidateTopologyDevice(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Provisioning", "all")

	net := testutil.LabNetwork(t)

	if !net.HasTopology() {
		testutil.TrackComment(t, "no topology loaded")
		t.Skip("no topology.json in lab specs")
	}

	tp, err := network.NewTopologyProvisioner(net)
	if err != nil {
		t.Fatalf("NewTopologyProvisioner: %v", err)
	}

	// Validate each SONiC device succeeds
	nodes := testutil.LabSonicNodes(t)
	for _, node := range nodes {
		t.Run(node.Name, func(t *testing.T) {
			if err := tp.ValidateTopologyDevice(node.Name); err != nil {
				t.Errorf("ValidateTopologyDevice(%q): %v", node.Name, err)
			}
		})
	}

	// Validate a non-existent device returns error
	t.Run("nonexistent", func(t *testing.T) {
		if err := tp.ValidateTopologyDevice("no-such-device"); err == nil {
			t.Error("expected error for non-existent device, got nil")
		}
	})
}

func TestE2E_GenerateDeviceComposite(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Provisioning", "leaf")

	net := testutil.LabNetwork(t)

	if !net.HasTopology() {
		testutil.TrackComment(t, "no topology loaded")
		t.Skip("no topology.json in lab specs")
	}

	tp, err := network.NewTopologyProvisioner(net)
	if err != nil {
		t.Fatalf("NewTopologyProvisioner: %v", err)
	}

	nodeName := leafNodeName(t)
	composite, err := tp.GenerateDeviceComposite(nodeName)
	if err != nil {
		t.Fatalf("GenerateDeviceComposite(%q): %v", nodeName, err)
	}

	// Assert composite has expected tables
	requiredTables := []string{"DEVICE_METADATA", "LOOPBACK_INTERFACE", "PORT", "BGP_GLOBALS", "BGP_NEIGHBOR", "NEWTRON_SERVICE_BINDING"}
	for _, table := range requiredTables {
		if _, ok := composite.Tables[table]; !ok {
			t.Errorf("composite missing table %q", table)
		}
	}

	// Assert DEVICE_METADATA|localhost has correct hostname
	if meta, ok := composite.Tables["DEVICE_METADATA"]; ok {
		if localhost, ok := meta["localhost"]; ok {
			if localhost["hostname"] != nodeName {
				t.Errorf("hostname = %q, want %q", localhost["hostname"], nodeName)
			}
		} else {
			t.Error("DEVICE_METADATA|localhost not found")
		}
	}

	t.Logf("composite for %s: %d tables", nodeName, len(composite.Tables))
	for table, entries := range composite.Tables {
		t.Logf("  %s: %d entries", table, len(entries))
	}
}

func TestE2E_GenerateSpineComposite(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Provisioning", "spine")

	net := testutil.LabNetwork(t)

	if !net.HasTopology() {
		testutil.TrackComment(t, "no topology loaded")
		t.Skip("no topology.json in lab specs")
	}

	tp, err := network.NewTopologyProvisioner(net)
	if err != nil {
		t.Fatalf("NewTopologyProvisioner: %v", err)
	}

	nodeName := spineNodeName(t)
	composite, err := tp.GenerateDeviceComposite(nodeName)
	if err != nil {
		t.Fatalf("GenerateDeviceComposite(%q): %v", nodeName, err)
	}

	// Check for route_reflector_client in BGP_NEIGHBOR_AF entries
	if neighborAF, ok := composite.Tables["BGP_NEIGHBOR_AF"]; ok {
		foundRRC := false
		for key, fields := range neighborAF {
			if fields["route_reflector_client"] == "true" {
				foundRRC = true
				t.Logf("  BGP_NEIGHBOR_AF %s: route_reflector_client=true", key)
			}
		}
		if !foundRRC {
			t.Error("no BGP_NEIGHBOR_AF entries with route_reflector_client=true (expected for spine/RR)")
		}
	} else {
		t.Error("composite missing BGP_NEIGHBOR_AF table")
	}

	// Check ROUTE_REDISTRIBUTE entries
	if redistribute, ok := composite.Tables["ROUTE_REDISTRIBUTE"]; ok {
		if len(redistribute) == 0 {
			t.Error("ROUTE_REDISTRIBUTE table is empty")
		}
		for key := range redistribute {
			t.Logf("  ROUTE_REDISTRIBUTE: %s", key)
		}
	} else {
		t.Error("composite missing ROUTE_REDISTRIBUTE table")
	}
}

func TestE2E_ProvisionInterfaceFromTopology(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Provisioning", "leaf")

	net := testutil.LabNetwork(t)

	if !net.HasTopology() {
		testutil.TrackComment(t, "no topology loaded")
		t.Skip("no topology.json in lab specs")
	}

	tp, err := network.NewTopologyProvisioner(net)
	if err != nil {
		t.Fatalf("NewTopologyProvisioner: %v", err)
	}

	nodeName := leafNodeName(t)
	composite, err := tp.GenerateDeviceComposite(nodeName)
	if err != nil {
		t.Fatalf("GenerateDeviceComposite(%q): %v", nodeName, err)
	}

	// Inspect Ethernet0 entries (fabric-underlay with BGP)
	// Check INTERFACE|Ethernet0|<ip>/31 exists
	if intfTable, ok := composite.Tables["INTERFACE"]; ok {
		foundEth0IP := false
		for key := range intfTable {
			if len(key) > 9 && key[:9] == "Ethernet0" {
				foundEth0IP = true
				t.Logf("  INTERFACE entry: %s", key)
			}
		}
		if !foundEth0IP {
			t.Error("no INTERFACE entry for Ethernet0 found")
		}
	} else {
		t.Error("composite missing INTERFACE table")
	}

	// Check BGP_NEIGHBOR entry with correct peer ASN
	if bgpNeighbor, ok := composite.Tables["BGP_NEIGHBOR"]; ok {
		foundNeighbor := false
		for key, fields := range bgpNeighbor {
			if fields["asn"] != "" {
				foundNeighbor = true
				t.Logf("  BGP_NEIGHBOR %s: asn=%s", key, fields["asn"])
			}
		}
		if !foundNeighbor {
			t.Error("no BGP_NEIGHBOR entry with ASN found")
		}
	} else {
		t.Error("composite missing BGP_NEIGHBOR table")
	}
}

func TestE2E_ProvisionDeviceDelivery(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Provisioning", "leaf")

	// Destructive test — skip unless explicitly enabled
	if os.Getenv("NEWTRON_E2E_DESTRUCTIVE") != "1" {
		testutil.TrackComment(t, "skipped: NEWTRON_E2E_DESTRUCTIVE!=1")
		t.Skip("destructive test: set NEWTRON_E2E_DESTRUCTIVE=1 to enable")
	}

	net := testutil.LabNetwork(t)

	if !net.HasTopology() {
		testutil.TrackComment(t, "no topology loaded")
		t.Skip("no topology.json in lab specs")
	}

	tp, err := network.NewTopologyProvisioner(net)
	if err != nil {
		t.Fatalf("NewTopologyProvisioner: %v", err)
	}

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Cleanup: re-provision to restore original state
	t.Cleanup(func() {
		cleanCtx := testutil.LabContext(t)
		if _, err := tp.ProvisionDevice(cleanCtx, nodeName); err != nil {
			t.Logf("cleanup: re-provision %s failed: %v", nodeName, err)
		}
	})

	result, err := tp.ProvisionDevice(ctx, nodeName)
	if err != nil {
		t.Fatalf("ProvisionDevice(%q): %v", nodeName, err)
	}

	t.Logf("provisioned %s: applied=%d, skipped=%d, failed=%d",
		nodeName, result.Applied, result.Skipped, result.Failed)

	if result.Failed > 0 {
		t.Errorf("provisioning had %d failed entries", result.Failed)
	}

	// Verify key CONFIG_DB entries
	testutil.AssertConfigDBEntry(t, nodeName, "DEVICE_METADATA", "localhost", map[string]string{
		"hostname": nodeName,
	})
	testutil.AssertConfigDBEntryExists(t, nodeName, "BGP_GLOBALS", "default")
}
