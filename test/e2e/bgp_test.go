//go:build e2e

package e2e_test

import (
	"context"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/network"
)

func TestE2E_BGPGlobalsConfig(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "BGP", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Snapshot current BGP_GLOBALS for restoration
	snapshotClient := testutil.LabRedisClient(t, nodeName, 4)
	originalFields, err := snapshotClient.HGetAll(context.Background(), "BGP_GLOBALS|default").Result()
	if err != nil {
		t.Fatalf("reading BGP_GLOBALS snapshot: %v", err)
	}

	// Cleanup: restore original BGP_GLOBALS
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "BGP_GLOBALS|default")
		if len(originalFields) > 0 {
			args := make([]interface{}, 0, len(originalFields)*2)
			for k, v := range originalFields {
				args = append(args, k, v)
			}
			client.HSet(c, "BGP_GLOBALS|default", args...)
		}
	})

	dev := testutil.LabLockedDevice(t, nodeName)
	cs, err := dev.SetBGPGlobals(ctx, network.BGPGlobalsConfig{
		LocalASN:           65199,
		RouterID:           "10.255.255.99",
		LogNeighborChanges: true,
	})
	if err != nil {
		t.Fatalf("SetBGPGlobals: %v", err)
	}
	if err := cs.Apply(dev); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	testutil.AssertConfigDBEntry(t, nodeName, "BGP_GLOBALS", "default", map[string]string{
		"local_asn":            "65199",
		"router_id":            "10.255.255.99",
		"log_neighbor_changes": "true",
	})
}

func TestE2E_RouteRedistribution(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "BGP", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Cleanup: Redis DEL fallback
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		client.Del(context.Background(), "ROUTE_REDISTRIBUTE|default|static|bgp|ipv4")
	})

	// Add route redistribution
	dev := testutil.LabLockedDevice(t, nodeName)
	cs, err := dev.AddRouteRedistribution(ctx, network.RouteRedistributionConfig{
		SrcProtocol:   "static",
		AddressFamily: "ipv4",
	})
	if err != nil {
		t.Fatalf("AddRouteRedistribution: %v", err)
	}
	if err := cs.Apply(dev); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify entry exists
	testutil.AssertConfigDBEntryExists(t, nodeName, "ROUTE_REDISTRIBUTE", "default|static|bgp|ipv4")

	// Remove via fresh locked device
	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		return d.RemoveRouteRedistribution(ctx, "default", "static", "ipv4")
	})

	// Verify removal on a fresh connection (after cleanup runs)
	// Since LabCleanupChanges runs in t.Cleanup, verify removal inline with a fresh device
	rmDev := testutil.LabLockedDevice(t, nodeName)
	rmCS, err := rmDev.RemoveRouteRedistribution(ctx, "default", "static", "ipv4")
	if err != nil {
		t.Fatalf("RemoveRouteRedistribution: %v", err)
	}
	if err := rmCS.Apply(rmDev); err != nil {
		t.Fatalf("Apply remove: %v", err)
	}

	testutil.AssertConfigDBEntryAbsent(t, nodeName, "ROUTE_REDISTRIBUTE", "default|static|bgp|ipv4")
}

func TestE2E_AddRouteMap(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "BGP", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Cleanup: Redis DEL fallback
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		client.Del(context.Background(), "ROUTE_MAP|E2E_TEST_RM|10")
	})

	// Add route-map
	dev := testutil.LabLockedDevice(t, nodeName)
	cs, err := dev.AddRouteMap(ctx, network.RouteMapConfig{
		Name:         "E2E_TEST_RM",
		Sequence:     10,
		Action:       "permit",
		SetLocalPref: 200,
	})
	if err != nil {
		t.Fatalf("AddRouteMap: %v", err)
	}
	if err := cs.Apply(dev); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify
	testutil.AssertConfigDBEntry(t, nodeName, "ROUTE_MAP", "E2E_TEST_RM|10", map[string]string{
		"route_operation": "permit",
		"set_local_pref":  "200",
	})

	// Delete route-map
	delDev := testutil.LabLockedDevice(t, nodeName)
	delCS, err := delDev.DeleteRouteMap(ctx, "E2E_TEST_RM")
	if err != nil {
		t.Fatalf("DeleteRouteMap: %v", err)
	}
	if err := delCS.Apply(delDev); err != nil {
		t.Fatalf("Apply delete: %v", err)
	}

	testutil.AssertConfigDBEntryAbsent(t, nodeName, "ROUTE_MAP", "E2E_TEST_RM|10")
}

func TestE2E_BGPNetwork(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "BGP", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Cleanup: Redis DEL fallback
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		client.Del(context.Background(), "BGP_GLOBALS_AF_NETWORK|default|ipv4_unicast|10.99.0.0/24")
	})

	// Add BGP network
	dev := testutil.LabLockedDevice(t, nodeName)
	cs, err := dev.AddBGPNetwork(ctx, "default", "ipv4_unicast", "10.99.0.0/24", "")
	if err != nil {
		t.Fatalf("AddBGPNetwork: %v", err)
	}
	if err := cs.Apply(dev); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify
	testutil.AssertConfigDBEntryExists(t, nodeName, "BGP_GLOBALS_AF_NETWORK", "default|ipv4_unicast|10.99.0.0/24")

	// Remove BGP network
	rmDev := testutil.LabLockedDevice(t, nodeName)
	rmCS, err := rmDev.RemoveBGPNetwork(ctx, "default", "ipv4_unicast", "10.99.0.0/24")
	if err != nil {
		t.Fatalf("RemoveBGPNetwork: %v", err)
	}
	if err := rmCS.Apply(rmDev); err != nil {
		t.Fatalf("Apply remove: %v", err)
	}

	testutil.AssertConfigDBEntryAbsent(t, nodeName, "BGP_GLOBALS_AF_NETWORK", "default|ipv4_unicast|10.99.0.0/24")
}
