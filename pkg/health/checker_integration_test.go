//go:build integration

package health_test

import (
	"context"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/health"
)

// Seed data summary (from configdb.json + statedb.json):
//
//   PORT table: Ethernet0..Ethernet7 (8 ports total)
//     - Ethernet0..Ethernet5: admin_status=up, oper_status=up   (6 up)
//     - Ethernet6, Ethernet7: admin_status=down, oper_status=down (2 down)
//   PORTCHANNEL: PortChannel100 (admin_status=up, 2 members: Ethernet4, Ethernet5)
//     LAG_TABLE: PortChannel100 oper_status=up, both members up+selected
//   BGP_NEIGHBOR: 10.0.0.1, 10.0.0.2
//     BGP_NEIGHBOR_TABLE: both Established
//   VXLAN_TUNNEL: vtep1 (src_ip=10.0.0.10)
//     VXLAN_TUNNEL_TABLE: vtep1 operstatus=up
//   VXLAN_TUNNEL_MAP: 2 entries (Vlan100 L2VNI, Vrf_CUST1 L3VNI)

// ---------------------------------------------------------------------------
// TestInterfaceCheck runs the interface health check and verifies
// the result reflects the seed data: 6 up ports, 2 admin-down ports.
// The InterfaceCheck counts ports where admin_status=up AND oper_status=down.
// In our seed data all admin-up ports are also oper-up, so downCount = 0.
// ---------------------------------------------------------------------------
func TestInterfaceCheck(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	ctx := testutil.Context(t)

	check := &health.InterfaceCheck{}
	result := check.Run(ctx, dev)

	if result.Check != "interfaces" {
		t.Errorf("Check = %q, want %q", result.Check, "interfaces")
	}
	if result.Status != health.StatusOK {
		t.Errorf("Status = %q, want %q", result.Status, health.StatusOK)
	}

	details, ok := result.Details.(map[string]int)
	if !ok {
		t.Fatalf("Details is not map[string]int, got %T", result.Details)
	}

	// 8 physical ports + 1 PortChannel = 9 interfaces from ListInterfaces
	totalInterfaces := details["total"]
	if totalInterfaces != 9 {
		t.Errorf("total interfaces = %d, want 9", totalInterfaces)
	}

	// No admin-up/oper-down ports in seed data (the 2 down ports are admin-down too)
	if details["down"] != 0 {
		t.Errorf("down = %d, want 0", details["down"])
	}
}

// ---------------------------------------------------------------------------
// TestLAGCheck verifies PortChannel100 is healthy (all members active).
// ---------------------------------------------------------------------------
func TestLAGCheck(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	ctx := testutil.Context(t)

	check := &health.LAGCheck{}
	result := check.Run(ctx, dev)

	if result.Check != "lag" {
		t.Errorf("Check = %q, want %q", result.Check, "lag")
	}
	if result.Status != health.StatusOK {
		t.Errorf("Status = %q, want %q", result.Status, health.StatusOK)
	}

	details, ok := result.Details.(map[string]int)
	if !ok {
		t.Fatalf("Details is not map[string]int, got %T", result.Details)
	}
	if details["total"] != 1 {
		t.Errorf("total LAGs = %d, want 1", details["total"])
	}
	if details["degraded"] != 0 {
		t.Errorf("degraded = %d, want 0", details["degraded"])
	}
}

// ---------------------------------------------------------------------------
// TestBGPCheck verifies BGP neighbors are healthy.
// The BGPCheck in the current implementation returns "BGP peers healthy"
// as a placeholder when BGP is configured.
// ---------------------------------------------------------------------------
func TestBGPCheck(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	ctx := testutil.Context(t)

	check := &health.BGPCheck{}
	result := check.Run(ctx, dev)

	if result.Check != "bgp" {
		t.Errorf("Check = %q, want %q", result.Check, "bgp")
	}
	// BGP is configured (2 neighbors in config_db), so it should check peers
	if result.Status != health.StatusOK {
		t.Errorf("Status = %q, want %q", result.Status, health.StatusOK)
	}
}

// ---------------------------------------------------------------------------
// TestVXLANCheck verifies VTEP health.
// Seed data has vtep1 configured and operationally up.
// ---------------------------------------------------------------------------
func TestVXLANCheck(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	ctx := testutil.Context(t)

	check := &health.VXLANCheck{}
	result := check.Run(ctx, dev)

	if result.Check != "vxlan" {
		t.Errorf("Check = %q, want %q", result.Check, "vxlan")
	}
	// VTEP exists in config_db, so it should report operational
	if result.Status != health.StatusOK {
		t.Errorf("Status = %q, want %q", result.Status, health.StatusOK)
	}
}

// ---------------------------------------------------------------------------
// TestEVPNCheck verifies EVPN route health.
// Both VTEP and BGP are configured, so EVPN check should execute.
// ---------------------------------------------------------------------------
func TestEVPNCheck(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	ctx := testutil.Context(t)

	check := &health.EVPNCheck{}
	result := check.Run(ctx, dev)

	if result.Check != "evpn" {
		t.Errorf("Check = %q, want %q", result.Check, "evpn")
	}
	if result.Status != health.StatusOK {
		t.Errorf("Status = %q, want %q", result.Status, health.StatusOK)
	}
}

// ---------------------------------------------------------------------------
// TestCheckerRun runs the full Checker.Run() and verifies all 5 results
// are returned with the device name and an OK overall status.
// ---------------------------------------------------------------------------
func TestCheckerRun(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	ctx := testutil.Context(t)

	checker := health.NewChecker()
	report, err := checker.Run(ctx, dev)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if report.Device != "test-leaf1" {
		t.Errorf("Device = %q, want %q", report.Device, "test-leaf1")
	}
	if report.Overall != health.StatusOK {
		t.Errorf("Overall = %q, want %q", report.Overall, health.StatusOK)
	}
	if len(report.Results) != 5 {
		t.Errorf("Results count = %d, want 5", len(report.Results))
	}
	if report.Duration <= 0 {
		t.Error("Duration should be positive")
	}

	// All individual results should be OK with our clean seed data
	for _, r := range report.Results {
		if r.Status != health.StatusOK {
			t.Errorf("Result %q: Status = %q, want %q", r.Check, r.Status, health.StatusOK)
		}
	}
}

// ---------------------------------------------------------------------------
// TestRunCheckByName verifies running a single check by name.
// ---------------------------------------------------------------------------
func TestRunCheckByName(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	ctx := testutil.Context(t)

	checker := health.NewChecker()

	t.Run("existing check", func(t *testing.T) {
		result, err := checker.RunCheck(ctx, dev, "interfaces")
		if err != nil {
			t.Fatalf("RunCheck() error: %v", err)
		}
		if result.Check != "interfaces" {
			t.Errorf("Check = %q, want %q", result.Check, "interfaces")
		}
		if result.Status != health.StatusOK {
			t.Errorf("Status = %q, want %q", result.Status, health.StatusOK)
		}
	})

	t.Run("non-existent check", func(t *testing.T) {
		_, err := checker.RunCheck(ctx, dev, "nonexistent")
		if err == nil {
			t.Fatal("RunCheck() should return error for unknown check")
		}
	})
}

// ---------------------------------------------------------------------------
// TestHealthCheckWithDegradedState modifies state_db to make a port
// that is admin-up appear as oper-down, reconnects, and verifies the
// interface check detects the degraded port.
// ---------------------------------------------------------------------------
func TestHealthCheckWithDegradedState(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()

	// Modify state_db: set Ethernet0 oper_status to "down" while admin_status remains "up"
	// This creates an admin-up/oper-down condition that the InterfaceCheck flags.
	testutil.WriteSingleEntry(t, addr, 6, "PORT_TABLE", "Ethernet0", map[string]string{
		"admin_status": "up",
		"oper_status":  "down",
		"speed":        "40000",
		"mtu":          "9100",
	})

	// Connect a fresh network device so it picks up the modified state_db
	net := testutil.TestNetwork(t)
	ctx := testutil.Context(t)

	dev, err := net.ConnectDevice(ctx, "test-leaf1")
	if err != nil {
		t.Fatalf("connecting device: %v", err)
	}
	t.Cleanup(func() { dev.Disconnect() })

	check := &health.InterfaceCheck{}
	result := check.Run(context.Background(), dev)

	details, ok := result.Details.(map[string]int)
	if !ok {
		t.Fatalf("Details is not map[string]int, got %T", result.Details)
	}

	// Now we should see at least 1 admin-up/oper-down interface
	if details["down"] < 1 {
		t.Errorf("down = %d, want >= 1 (Ethernet0 should be admin-up/oper-down)", details["down"])
	}

	// Status should be Warning (some interfaces down but not majority)
	if result.Status != health.StatusWarning {
		t.Errorf("Status = %q, want %q", result.Status, health.StatusWarning)
	}
}
