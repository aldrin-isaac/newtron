//go:build integration

package operations_test

import (
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/operations"
)

// =============================================================================
// PreconditionChecker Tests
// =============================================================================

func TestRequireConnected_Pass(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test", "resource")
	checker.RequireConnected()

	if checker.HasErrors() {
		t.Fatalf("expected no errors for connected device, got: %v", checker.Errors())
	}
	if err := checker.Result(); err != nil {
		t.Fatalf("expected nil result, got: %v", err)
	}
}

func TestRequireConnected_Fail(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	// Get a device from TestNetwork but do NOT call Connect
	net := testutil.TestNetwork(t)
	dev, err := net.GetDevice("test-leaf1")
	if err != nil {
		t.Fatalf("getting device: %v", err)
	}

	checker := operations.NewPreconditionChecker(dev, "test", "resource")
	checker.RequireConnected()

	if !checker.HasErrors() {
		t.Fatal("expected errors for non-connected device, got none")
	}
	if err := checker.Result(); err == nil {
		t.Fatal("expected non-nil result for non-connected device")
	}
}

func TestRequireLocked_Pass(t *testing.T) {
	dev := testutil.LockedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test", "resource")
	checker.RequireLocked()

	if checker.HasErrors() {
		t.Fatalf("expected no errors for locked device, got: %v", checker.Errors())
	}
}

func TestRequireLocked_Fail(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)
	// Device is connected but NOT locked

	checker := operations.NewPreconditionChecker(dev, "test", "resource")
	checker.RequireLocked()

	if !checker.HasErrors() {
		t.Fatal("expected errors for non-locked device, got none")
	}
	if err := checker.Result(); err == nil {
		t.Fatal("expected non-nil result for non-locked device")
	}
}

func TestRequireInterfaceExists_Pass(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test", "Ethernet0")
	checker.RequireInterfaceExists("Ethernet0")

	if checker.HasErrors() {
		t.Fatalf("expected no errors for existing interface Ethernet0, got: %v", checker.Errors())
	}
}

func TestRequireInterfaceExists_Fail(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test", "Ethernet99")
	checker.RequireInterfaceExists("Ethernet99")

	if !checker.HasErrors() {
		t.Fatal("expected errors for non-existing interface Ethernet99, got none")
	}
}

func TestRequireVLANExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("VLAN100_exists", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Vlan100")
		checker.RequireVLANExists(100)

		if checker.HasErrors() {
			t.Fatalf("expected no errors for existing VLAN 100, got: %v", checker.Errors())
		}
	})

	t.Run("VLAN999_not_exists", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Vlan999")
		checker.RequireVLANExists(999)

		if !checker.HasErrors() {
			t.Fatal("expected errors for non-existing VLAN 999, got none")
		}
	})
}

func TestRequireVRFExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("Vrf_CUST1_exists", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Vrf_CUST1")
		checker.RequireVRFExists("Vrf_CUST1")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for existing VRF Vrf_CUST1, got: %v", checker.Errors())
		}
	})

	t.Run("NonexistentVRF_not_exists", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "NonexistentVRF")
		checker.RequireVRFExists("NonexistentVRF")

		if !checker.HasErrors() {
			t.Fatal("expected errors for non-existing VRF, got none")
		}
	})
}

func TestRequirePortChannelExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test", "PortChannel100")
	checker.RequirePortChannelExists("PortChannel100")

	if checker.HasErrors() {
		t.Fatalf("expected no errors for existing PortChannel100, got: %v", checker.Errors())
	}
}

func TestRequireVTEPConfigured(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test", "vtep")
	checker.RequireVTEPConfigured()

	if checker.HasErrors() {
		t.Fatalf("expected no errors for configured VTEP, got: %v", checker.Errors())
	}
}

func TestRequireBGPConfigured(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test", "bgp")
	checker.RequireBGPConfigured()

	if checker.HasErrors() {
		t.Fatalf("expected no errors for configured BGP, got: %v", checker.Errors())
	}
}

func TestRequireACLTableExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("existing_table", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "customer-l3-in")
		checker.RequireACLTableExists("customer-l3-in")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for existing ACL table, got: %v", checker.Errors())
		}
	})

	t.Run("nonexistent_table", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "nonexistent-acl")
		checker.RequireACLTableExists("nonexistent-acl")

		if !checker.HasErrors() {
			t.Fatal("expected errors for non-existing ACL table, got none")
		}
	})
}

func TestRequireInterfaceNotLAGMember(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	// Ethernet0 is NOT a LAG member, so RequireInterfaceNotLAGMember should pass
	checker := operations.NewPreconditionChecker(dev, "test", "Ethernet0")
	checker.RequireInterfaceNotLAGMember("Ethernet0")

	if checker.HasErrors() {
		t.Fatalf("expected no errors for non-LAG-member Ethernet0, got: %v", checker.Errors())
	}
}

func TestRequireInterfaceIsLAGMember(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("Ethernet4_is_member_of_PortChannel100", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Ethernet4")
		checker.RequireInterfaceIsLAGMember("Ethernet4", "PortChannel100")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for Ethernet4 as member of PortChannel100, got: %v", checker.Errors())
		}
	})

	t.Run("Ethernet0_is_not_member", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Ethernet0")
		checker.RequireInterfaceIsLAGMember("Ethernet0", "PortChannel100")

		if !checker.HasErrors() {
			t.Fatal("expected errors for Ethernet0 not being a LAG member, got none")
		}
	})
}

func TestRequireInterfaceNoService(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("Ethernet0_no_service", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Ethernet0")
		checker.RequireInterfaceNoService("Ethernet0")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for Ethernet0 (no service), got: %v", checker.Errors())
		}
	})

	t.Run("Ethernet1_has_service", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Ethernet1")
		checker.RequireInterfaceNoService("Ethernet1")

		if !checker.HasErrors() {
			t.Fatal("expected errors for Ethernet1 (has customer-l3 service), got none")
		}
	})
}

func TestRequireServiceExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("customer-l3_exists", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "customer-l3")
		checker.RequireServiceExists("customer-l3")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for existing service customer-l3, got: %v", checker.Errors())
		}
	})

	t.Run("nonexistent_service", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "nonexistent-service")
		checker.RequireServiceExists("nonexistent-service")

		if !checker.HasErrors() {
			t.Fatal("expected errors for non-existing service, got none")
		}
	})
}

func TestChainedChecks(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	checker := operations.NewPreconditionChecker(dev, "test.chained", "multi-resource")

	// These should pass:
	checker.RequireConnected()
	checker.RequireInterfaceExists("Ethernet0")
	checker.RequireVLANExists(100)

	// These should fail:
	checker.RequireVLANExists(999)         // VLAN 999 does not exist
	checker.RequireVRFExists("NoSuchVRF")  // VRF does not exist
	checker.RequireInterfaceExists("Eth99") // Interface does not exist

	if !checker.HasErrors() {
		t.Fatal("expected errors from chained checks with failures, got none")
	}

	errs := checker.Errors()
	if len(errs) != 3 {
		t.Fatalf("expected 3 errors from chained checks, got %d: %v", len(errs), errs)
	}

	// Result should aggregate all errors
	result := checker.Result()
	if result == nil {
		t.Fatal("expected non-nil result from chained checks with failures")
	}
}

// =============================================================================
// DependencyChecker Tests
// =============================================================================

func TestDependencyChecker_IsLastACLUser(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	// Ethernet1 is the only port on customer-l3-in ACL.
	// Excluding Ethernet1 means there are zero remaining ports.
	dc := operations.NewDependencyChecker(dev, "Ethernet1")

	if !dc.IsLastACLUser("customer-l3-in") {
		t.Fatal("expected Ethernet1 to be the last ACL user of customer-l3-in")
	}

	// If we exclude a different interface (e.g., Ethernet0), Ethernet1 remains
	dcOther := operations.NewDependencyChecker(dev, "Ethernet0")

	if dcOther.IsLastACLUser("customer-l3-in") {
		t.Fatal("expected Ethernet1 to still be on customer-l3-in when excluding Ethernet0")
	}
}

func TestDependencyChecker_IsLastServiceUser(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	// Ethernet1 is the only user of customer-l3 service (via NEWTRON_SERVICE_BINDING).
	dc := operations.NewDependencyChecker(dev, "Ethernet1")

	if !dc.IsLastServiceUser("customer-l3") {
		t.Fatal("expected Ethernet1 to be the last service user of customer-l3")
	}

	// Excluding a different interface should not change the result
	dcOther := operations.NewDependencyChecker(dev, "Ethernet0")

	if dcOther.IsLastServiceUser("customer-l3") {
		t.Fatal("expected customer-l3 to still have users when excluding Ethernet0")
	}
}

func TestDependencyChecker_GetACLRemainingPorts(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	// customer-l3-in has ports: "Ethernet1"
	// Excluding Ethernet1 should leave empty
	dc := operations.NewDependencyChecker(dev, "Ethernet1")

	remaining := dc.GetACLRemainingPorts("customer-l3-in")
	if remaining != "" {
		t.Fatalf("expected empty remaining ports after excluding Ethernet1, got: %q", remaining)
	}

	// Excluding a different interface should leave Ethernet1
	dcOther := operations.NewDependencyChecker(dev, "Ethernet0")

	remaining = dcOther.GetACLRemainingPorts("customer-l3-in")
	if remaining != "Ethernet1" {
		t.Fatalf("expected remaining ports to be 'Ethernet1' when excluding Ethernet0, got: %q", remaining)
	}
}

func TestDependencyChecker_IsLastVRFUser(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	// Ethernet1 is bound to Vrf_CUST1 (via INTERFACE table with vrf_name).
	// Check if Ethernet1 is the last user of Vrf_CUST1.
	dc := operations.NewDependencyChecker(dev, "Ethernet1")

	isLast := dc.IsLastVRFUser("Vrf_CUST1")
	// Ethernet1 has vrf_name=Vrf_CUST1 in INTERFACE table.
	// Whether it is the "last" depends on if other interfaces are also
	// in Vrf_CUST1. Based on seed data, only Ethernet1 has vrf_name set.
	if !isLast {
		t.Fatal("expected Ethernet1 to be the last VRF user of Vrf_CUST1")
	}

	// Excluding Ethernet0 (which is NOT in Vrf_CUST1) should not affect the result.
	dcOther := operations.NewDependencyChecker(dev, "Ethernet0")
	if dcOther.IsLastVRFUser("Vrf_CUST1") {
		t.Fatal("expected Vrf_CUST1 to still have users when excluding Ethernet0")
	}
}

func TestDependencyChecker_IsLastVLANMember(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	// VLAN 100 has members: Ethernet2 (untagged) and Ethernet3 (tagged).
	// Excluding Ethernet2 should leave Ethernet3 -> not last.
	dc := operations.NewDependencyChecker(dev, "Ethernet2")

	if dc.IsLastVLANMember(100) {
		t.Fatal("expected Ethernet2 NOT to be the last VLAN 100 member (Ethernet3 remains)")
	}

	// VLAN 200 has member: PortChannel100 (tagged).
	// Excluding PortChannel100 should leave empty -> last.
	dcPC := operations.NewDependencyChecker(dev, "PortChannel100")

	if !dcPC.IsLastVLANMember(200) {
		t.Fatal("expected PortChannel100 to be the last VLAN 200 member")
	}
}

// =============================================================================
// Custom Check Test
// =============================================================================

func TestCheck_CustomCondition(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("condition_true", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "resource")
		checker.Check(true, "custom condition", "should pass")

		if checker.HasErrors() {
			t.Fatalf("expected no errors when condition is true, got: %v", checker.Errors())
		}
	})

	t.Run("condition_false", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "resource")
		checker.Check(false, "custom condition", "expected to fail")

		if !checker.HasErrors() {
			t.Fatal("expected errors when condition is false, got none")
		}
	})
}

// =============================================================================
// RequireFilterSpecExists Test
// =============================================================================

func TestRequireFilterSpecExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("existing_filter_spec", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "customer-edge-in")
		checker.RequireFilterSpecExists("customer-edge-in")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for existing filter spec, got: %v", checker.Errors())
		}
	})

	t.Run("nonexistent_filter_spec", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "nonexistent-filter")
		checker.RequireFilterSpecExists("nonexistent-filter")

		if !checker.HasErrors() {
			t.Fatal("expected errors for non-existing filter spec, got none")
		}
	})
}

// =============================================================================
// Negative existence checks (RequireNotExists variants)
// =============================================================================

func TestRequireVLANNotExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("VLAN999_should_pass", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Vlan999")
		checker.RequireVLANNotExists(999)

		if checker.HasErrors() {
			t.Fatalf("expected no errors for non-existing VLAN 999, got: %v", checker.Errors())
		}
	})

	t.Run("VLAN100_should_fail", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Vlan100")
		checker.RequireVLANNotExists(100)

		if !checker.HasErrors() {
			t.Fatal("expected errors for existing VLAN 100, got none")
		}
	})
}

func TestRequirePortChannelNotExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("nonexistent_should_pass", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "PortChannel999")
		checker.RequirePortChannelNotExists("PortChannel999")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for non-existing PortChannel999, got: %v", checker.Errors())
		}
	})

	t.Run("existing_should_fail", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "PortChannel100")
		checker.RequirePortChannelNotExists("PortChannel100")

		if !checker.HasErrors() {
			t.Fatal("expected errors for existing PortChannel100, got none")
		}
	})
}

func TestRequireACLTableNotExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("nonexistent_should_pass", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "new-acl")
		checker.RequireACLTableNotExists("new-acl")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for non-existing ACL table, got: %v", checker.Errors())
		}
	})

	t.Run("existing_should_fail", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "customer-l3-in")
		checker.RequireACLTableNotExists("customer-l3-in")

		if !checker.HasErrors() {
			t.Fatal("expected errors for existing ACL table, got none")
		}
	})
}

func TestRequireVRFNotExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("nonexistent_should_pass", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "NewVrf")
		checker.RequireVRFNotExists("NewVrf")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for non-existing VRF, got: %v", checker.Errors())
		}
	})

	t.Run("existing_should_fail", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Vrf_CUST1")
		checker.RequireVRFNotExists("Vrf_CUST1")

		if !checker.HasErrors() {
			t.Fatal("expected errors for existing VRF, got none")
		}
	})
}

func TestRequireInterfaceNotExists(t *testing.T) {
	dev := testutil.ConnectedNetworkDevice(t)

	t.Run("nonexistent_should_pass", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Ethernet99")
		checker.RequireInterfaceNotExists("Ethernet99")

		if checker.HasErrors() {
			t.Fatalf("expected no errors for non-existing interface, got: %v", checker.Errors())
		}
	})

	t.Run("existing_should_fail", func(t *testing.T) {
		checker := operations.NewPreconditionChecker(dev, "test", "Ethernet0")
		checker.RequireInterfaceNotExists("Ethernet0")

		if !checker.HasErrors() {
			t.Fatal("expected errors for existing interface Ethernet0, got none")
		}
	})
}
