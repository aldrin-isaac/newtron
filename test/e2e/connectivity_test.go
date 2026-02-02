//go:build e2e

package e2e_test

import (
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
)

func TestE2E_ConnectAllNodes(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Connectivity", "all")

	nodes := testutil.LabSonicNodes(t)
	net := testutil.LabNetwork(t)
	ctx := testutil.LabContext(t)

	for _, node := range nodes {
		t.Run(node.Name, func(t *testing.T) {
			testutil.Track(t, "Connectivity", node.Name)
			dev, err := net.ConnectDevice(ctx, node.Name)
			if err != nil {
				t.Skipf("skipping %s: %v", node.Name, err)
			}
			defer dev.Disconnect()

			if !dev.IsConnected() {
				t.Fatalf("%s is not connected", node.Name)
			}
		})
	}
}

func TestE2E_VerifyStartupConfig(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Connectivity", "all")

	nodes := testutil.LabSonicNodes(t)
	for _, node := range nodes {
		t.Run(node.Name, func(t *testing.T) {
			testutil.Track(t, "Connectivity", node.Name)
			dev, err := testutil.TryLabConnectedDevice(t, node.Name)
			if err != nil {
				t.Skipf("skipping %s: %v", node.Name, err)
			}

			configDB := dev.ConfigDB()
			if configDB == nil {
				t.Fatalf("%s: config_db not loaded", node.Name)
			}

			// Verify hostname in DEVICE_METADATA matches the node name
			metadata, ok := configDB.DeviceMetadata["localhost"]
			if !ok {
				t.Fatalf("%s: DEVICE_METADATA|localhost not found", node.Name)
			}

			hostname, ok := metadata["hostname"]
			if !ok {
				t.Fatalf("%s: hostname field not found in DEVICE_METADATA", node.Name)
			}

			if hostname != node.Name {
				t.Errorf("%s: hostname = %q, want %q", node.Name, hostname, node.Name)
			}
		})
	}
}

func TestE2E_VerifyLoopbackInterface(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Connectivity", "all")

	nodes := testutil.LabSonicNodes(t)
	for _, node := range nodes {
		t.Run(node.Name, func(t *testing.T) {
			testutil.Track(t, "Connectivity", node.Name)
			dev, err := testutil.TryLabConnectedDevice(t, node.Name)
			if err != nil {
				t.Skipf("skipping %s: %v", node.Name, err)
			}

			configDB := dev.ConfigDB()
			if configDB == nil {
				t.Fatalf("%s: config_db not loaded", node.Name)
			}

			// Verify LOOPBACK_INTERFACE has at least Loopback0
			if len(configDB.LoopbackInterface) == 0 {
				t.Fatalf("%s: no loopback interfaces configured", node.Name)
			}

			// Check that at least one key starts with "Loopback0"
			found := false
			for key := range configDB.LoopbackInterface {
				if key == "Loopback0" || len(key) > 10 && key[:10] == "Loopback0|" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: Loopback0 not found in LOOPBACK_INTERFACE", node.Name)
			}
		})
	}
}

func TestE2E_ListInterfaces(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Connectivity", "all")

	nodes := testutil.LabSonicNodes(t)
	for _, node := range nodes {
		t.Run(node.Name, func(t *testing.T) {
			testutil.Track(t, "Connectivity", node.Name)
			dev, err := testutil.TryLabConnectedDevice(t, node.Name)
			if err != nil {
				t.Skipf("skipping %s: %v", node.Name, err)
			}

			ifaces := dev.ListInterfaces()
			if len(ifaces) == 0 {
				t.Fatalf("%s has no interfaces", node.Name)
			}
			t.Logf("%s: %d interfaces: %v", node.Name, len(ifaces), ifaces)

			// Verify we can get details for each interface
			for _, name := range ifaces {
				intf, err := dev.GetInterface(name)
				if err != nil {
					t.Errorf("%s: GetInterface(%s) failed: %v", node.Name, name, err)
					continue
				}
				t.Logf("  %s", intf)
			}
		})
	}
}
