package auth

import (
	"errors"
	"testing"

	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

func TestContext_Chaining(t *testing.T) {
	ctx := NewContext().
		WithDevice("leaf1-ny").
		WithService("customer-l3").
		WithInterface("Ethernet0").
		WithResource("vlan100")

	if ctx.Device != "leaf1-ny" {
		t.Errorf("Device = %q", ctx.Device)
	}
	if ctx.Service != "customer-l3" {
		t.Errorf("Service = %q", ctx.Service)
	}
	if ctx.Interface != "Ethernet0" {
		t.Errorf("Interface = %q", ctx.Interface)
	}
	if ctx.Resource != "vlan100" {
		t.Errorf("Resource = %q", ctx.Resource)
	}
}

func createTestNetworkSpec() *spec.NetworkSpecFile {
	return &spec.NetworkSpecFile{
		SuperUsers: []string{"admin", "root"},
		UserGroups: map[string][]string{
			"neteng": {"alice", "bob"},
			"netops": {"charlie", "diana"},
			"viewer": {"eve"},
		},
		Permissions: map[string][]string{
			"all":             {"neteng"},
			"service.apply":   {"neteng", "netops"},
			"service.remove":  {"neteng", "netops", "viewer"},
			"vlan.create":     {"neteng"},
			"baseline.apply":  {"neteng", "netops", "viewer"},
		},
		Services: map[string]*spec.ServiceSpec{
			"customer-l3": {
				Description: "Customer L3",
				Permissions: map[string][]string{
					"service.apply": {"netops"}, // More restrictive
				},
			},
			"transit": {
				Description: "Transit service",
				Permissions: map[string][]string{
					"all": {"neteng"}, // Only neteng
				},
			},
		},
	}
}

func TestChecker_SuperUser(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)
	checker.SetUser("admin")

	// Superuser should pass all checks
	if err := checker.Check(PermServiceApply, nil); err != nil {
		t.Errorf("Superuser should be allowed: %v", err)
	}
	if err := checker.Check(PermBaselineApply, nil); err != nil {
		t.Errorf("Superuser should be allowed: %v", err)
	}

	if !checker.IsSuperUser() {
		t.Error("admin should be superuser")
	}
}

func TestChecker_GlobalPermissions(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	t.Run("user in allowed group", func(t *testing.T) {
		checker.SetUser("alice") // In neteng
		if err := checker.Check(PermServiceApply, nil); err != nil {
			t.Errorf("alice (neteng) should have service.apply: %v", err)
		}
	})

	t.Run("user with 'all' permission", func(t *testing.T) {
		checker.SetUser("bob") // In neteng which has 'all'
		if err := checker.Check(PermVLANCreate, nil); err != nil {
			t.Errorf("bob (neteng with 'all') should have vlan.create: %v", err)
		}
	})

	t.Run("user without permission", func(t *testing.T) {
		checker.SetUser("eve") // In viewer only
		if err := checker.Check(PermServiceApply, nil); err == nil {
			t.Error("eve (viewer) should not have service.apply")
		}
	})

}

func TestChecker_ServicePermissions(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	t.Run("service-specific override", func(t *testing.T) {
		checker.SetUser("charlie") // In netops
		ctx := NewContext().WithService("customer-l3")

		// charlie should have service.apply for customer-l3 (service override)
		if err := checker.Check(PermServiceApply, ctx); err != nil {
			t.Errorf("charlie should have permission via service override: %v", err)
		}
	})

	t.Run("service with 'all' permission", func(t *testing.T) {
		checker.SetUser("alice") // In neteng
		ctx := NewContext().WithService("transit")

		// alice should have any permission on transit (service has 'all' for neteng)
		if err := checker.Check(PermServiceApply, ctx); err != nil {
			t.Errorf("alice should have permission via service 'all': %v", err)
		}
	})

	t.Run("no service permission falls back to global", func(t *testing.T) {
		checker.SetUser("diana") // In netops
		ctx := NewContext().WithService("transit")

		// diana is netops, transit has no netops permission, but global does
		if err := checker.Check(PermServiceApply, ctx); err != nil {
			t.Errorf("diana should have permission via global fallback: %v", err)
		}
	})
}

func TestChecker_PermissionError(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)
	checker.SetUser("eve")

	ctx := NewContext().WithService("customer-l3").WithDevice("leaf1-ny")
	err := checker.Check(PermServiceApply, ctx)

	if err == nil {
		t.Fatal("Expected error")
	}

	var permErr *PermissionError
	if !errors.As(err, &permErr) {
		t.Fatalf("Expected PermissionError, got %T", err)
	}

	if permErr.User != "eve" {
		t.Errorf("User = %q", permErr.User)
	}
	if permErr.Permission != PermServiceApply {
		t.Errorf("Permission = %q", permErr.Permission)
	}

	// Check error message
	msg := err.Error()
	if msg == "" {
		t.Error("Error message should not be empty")
	}

	// Check unwrap
	if !errors.Is(err, util.ErrPermissionDenied) {
		t.Error("Should unwrap to ErrPermissionDenied")
	}
}

func TestChecker_ListPermissions(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	t.Run("superuser", func(t *testing.T) {
		checker.SetUser("admin")
		perms := checker.ListPermissions()
		if len(perms) != 1 || perms[0] != PermAll {
			t.Errorf("Superuser should have PermAll only, got %v", perms)
		}
	})

	t.Run("regular user", func(t *testing.T) {
		checker.SetUser("eve") // In viewer
		perms := checker.ListPermissions()

		// eve should have service.remove and baseline.apply (via viewer group)
		permMap := make(map[Permission]bool)
		for _, p := range perms {
			permMap[p] = true
		}

		if !permMap[PermServiceRemove] {
			t.Error("eve should have service.remove")
		}
		if !permMap[PermBaselineApply] {
			t.Error("eve should have baseline.apply")
		}
		if permMap[PermServiceApply] {
			t.Error("eve should not have service.apply")
		}
	})
}

func TestChecker_GetUserGroups(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	groups := checker.GetUserGroups("alice")
	if len(groups) != 1 || groups[0] != "neteng" {
		t.Errorf("alice groups = %v, want [neteng]", groups)
	}

	groups = checker.GetUserGroups("unknown")
	if len(groups) != 0 {
		t.Errorf("unknown user should have no groups, got %v", groups)
	}
}

func TestChecker_DirectUserPermission(t *testing.T) {
	network := &spec.NetworkSpecFile{
		Permissions: map[string][]string{
			"service.apply": {"direct-user"}, // Direct user, not a group
		},
	}
	checker := NewChecker(network)
	checker.SetUser("direct-user")

	if err := checker.Check(PermServiceApply, nil); err != nil {
		t.Errorf("Direct user permission should work: %v", err)
	}
}

func TestChecker_CurrentUser(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	// Initially should have some username (from os/user)
	if checker.CurrentUser() == "" {
		t.Error("CurrentUser should not be empty after NewChecker")
	}

	// After SetUser, should return the set user
	checker.SetUser("test-user")
	if checker.CurrentUser() != "test-user" {
		t.Errorf("CurrentUser() = %q, want %q", checker.CurrentUser(), "test-user")
	}
}

func TestChecker_ServiceWithNilPermissions(t *testing.T) {
	network := &spec.NetworkSpecFile{
		SuperUsers: []string{},
		UserGroups: map[string][]string{
			"neteng": {"alice"},
		},
		Permissions: map[string][]string{
			"service.apply": {"neteng"},
		},
		Services: map[string]*spec.ServiceSpec{
			"no-perms-service": {
				Description: "Service with nil permissions",
				Permissions: nil, // Explicitly nil
			},
		},
	}
	checker := NewChecker(network)
	checker.SetUser("alice")

	// Should fall back to global permissions
	ctx := NewContext().WithService("no-perms-service")
	if err := checker.Check(PermServiceApply, ctx); err != nil {
		t.Errorf("Should fall back to global permission: %v", err)
	}
}

func TestChecker_GlobalPermissionNotFound(t *testing.T) {
	network := &spec.NetworkSpecFile{
		SuperUsers:  []string{},
		UserGroups:  map[string][]string{},
		Permissions: map[string][]string{}, // No permissions defined
	}
	checker := NewChecker(network)
	checker.SetUser("anyone")

	err := checker.Check(PermServiceApply, nil)
	if err == nil {
		t.Error("Should be denied when no permissions defined")
	}
}

func TestChecker_GlobalAllPermissionNotGranted(t *testing.T) {
	// Test case where 'all' permission exists but user is not in those groups
	network := &spec.NetworkSpecFile{
		SuperUsers: []string{},
		UserGroups: map[string][]string{
			"admins": {"admin-user"},
			"users":  {"normal-user"},
		},
		Permissions: map[string][]string{
			"all": {"admins"}, // Only admins have 'all'
		},
	}
	checker := NewChecker(network)
	checker.SetUser("normal-user")

	// normal-user should be denied (not in admins group)
	err := checker.Check(PermServiceApply, nil)
	if err == nil {
		t.Error("normal-user should not have permission via 'all'")
	}
}

func TestChecker_ServiceAllPermissionNotGranted(t *testing.T) {
	network := &spec.NetworkSpecFile{
		SuperUsers: []string{},
		UserGroups: map[string][]string{
			"admins": {"admin-user"},
			"users":  {"normal-user"},
		},
		Permissions: map[string][]string{},
		Services: map[string]*spec.ServiceSpec{
			"restricted": {
				Description: "Restricted service",
				Permissions: map[string][]string{
					"all": {"admins"}, // Only admins have 'all' on this service
				},
			},
		},
	}
	checker := NewChecker(network)
	checker.SetUser("normal-user")

	ctx := NewContext().WithService("restricted")
	err := checker.Check(PermServiceApply, ctx)
	if err == nil {
		t.Error("normal-user should not have permission via service 'all'")
	}
}

func TestPermissionError_ContextVariations(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		err := &PermissionError{
			User:       "alice",
			Permission: PermServiceApply,
			Context:    nil,
		}
		msg := err.Error()
		if msg == "" {
			t.Error("Error message should not be empty")
		}
		// Should not contain "for service" or "on device" when context is nil
		if contains(msg, "for service") || contains(msg, "on device") {
			t.Error("Should not mention 'for service'/'on device' when context is nil")
		}
	})

	t.Run("context with service only", func(t *testing.T) {
		err := &PermissionError{
			User:       "alice",
			Permission: PermServiceApply,
			Context:    &Context{Service: "test-svc"},
		}
		msg := err.Error()
		if !contains(msg, "test-svc") {
			t.Error("Should mention service name")
		}
	})

	t.Run("context with device only", func(t *testing.T) {
		err := &PermissionError{
			User:       "alice",
			Permission: PermServiceApply,
			Context:    &Context{Device: "leaf1"},
		}
		msg := err.Error()
		if !contains(msg, "leaf1") {
			t.Error("Should mention device name")
		}
	})

	t.Run("context with both service and device", func(t *testing.T) {
		err := &PermissionError{
			User:       "alice",
			Permission: PermServiceApply,
			Context:    &Context{Service: "svc1", Device: "dev1"},
		}
		msg := err.Error()
		if !contains(msg, "svc1") || !contains(msg, "dev1") {
			t.Error("Should mention both service and device")
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
