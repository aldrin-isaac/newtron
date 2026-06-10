package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// shorthand makes a spec.PermissionGrants with one grant whose
// Where clause is empty — the in-test equivalent of the pre-L5
// ["group1", "group2"] flat list. Tests that exercise the L5
// where-clause semantics construct PermissionGrant literals
// directly instead of using this helper.
func shorthand(groups ...string) spec.PermissionGrants {
	return spec.PermissionGrants{{Groups: groups}}
}

func TestContext_Chaining(t *testing.T) {
	ctx := NewContext().
		WithCaller("alice").
		WithDevice("leaf1-ny").
		WithService("customer-l3").
		WithInterface("Ethernet0").
		WithResource("vlan100")

	if ctx.Caller != "alice" {
		t.Errorf("Caller = %q", ctx.Caller)
	}
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
		Permissions: map[string]spec.PermissionGrants{
			"all":            shorthand("neteng"),
			"service.apply":  shorthand("neteng", "netops"),
			"service.remove": shorthand("neteng", "netops", "viewer"),
			"vlan.create":    shorthand("neteng"),
			"device.cleanup": shorthand("neteng", "netops", "viewer"),
		},
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{
				"customer-l3": {
					Description: "Customer L3",
					Permissions: map[string]spec.PermissionGrants{
						"service.apply": shorthand("netops"), // More restrictive
					},
				},
				"transit": {
					Description: "Transit service",
					Permissions: map[string]spec.PermissionGrants{
						"all": shorthand("neteng"), // Only neteng
					},
				},
			},
		},
	}
}

// callerCtx returns an auth Context for the given caller. Convenience
// wrapper so tests read as "alice asking about X" rather than
// boilerplate Context construction.
func callerCtx(caller string) *Context {
	return NewContext().WithCaller(caller)
}

func TestChecker_SuperUser(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	// Superuser should pass all checks
	if err := checker.Check(PermServiceApply, callerCtx("admin")); err != nil {
		t.Errorf("Superuser should be allowed: %v", err)
	}
	if !checker.isSuperUser("admin") {
		t.Error("admin should be superuser")
	}
}

func TestChecker_GlobalPermissions(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	t.Run("user in allowed group", func(t *testing.T) {
		if err := checker.Check(PermServiceApply, callerCtx("alice")); err != nil {
			t.Errorf("alice (neteng) should have service.apply: %v", err)
		}
	})

	t.Run("user with 'all' permission", func(t *testing.T) {
		if err := checker.Check(PermVLANCreate, callerCtx("bob")); err != nil {
			t.Errorf("bob (neteng with 'all') should have vlan.create: %v", err)
		}
	})

	t.Run("user without permission", func(t *testing.T) {
		if err := checker.Check(PermServiceApply, callerCtx("eve")); err == nil {
			t.Error("eve (viewer) should not have service.apply")
		}
	})
}

func TestChecker_ServicePermissions(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	t.Run("service-specific override", func(t *testing.T) {
		ctx := callerCtx("charlie").WithService("customer-l3")
		if err := checker.Check(PermServiceApply, ctx); err != nil {
			t.Errorf("charlie should have permission via service override: %v", err)
		}
	})

	t.Run("service with 'all' permission", func(t *testing.T) {
		ctx := callerCtx("alice").WithService("transit")
		if err := checker.Check(PermServiceApply, ctx); err != nil {
			t.Errorf("alice should have permission via service 'all': %v", err)
		}
	})

	t.Run("no service permission falls back to global", func(t *testing.T) {
		ctx := callerCtx("diana").WithService("transit")
		// diana is netops, transit has no netops permission, but global does
		if err := checker.Check(PermServiceApply, ctx); err != nil {
			t.Errorf("diana should have permission via global fallback: %v", err)
		}
	})
}

func TestChecker_PermissionError(t *testing.T) {
	network := createTestNetworkSpec()
	checker := NewChecker(network)

	ctx := callerCtx("eve").WithService("customer-l3").WithDevice("leaf1-ny")
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

func TestChecker_DirectUserPermission(t *testing.T) {
	network := &spec.NetworkSpecFile{
		Permissions: map[string]spec.PermissionGrants{
			"service.apply": shorthand("direct-user"), // Direct user, not a group
		},
	}
	checker := NewChecker(network)

	if err := checker.Check(PermServiceApply, callerCtx("direct-user")); err != nil {
		t.Errorf("Direct user permission should work: %v", err)
	}
}

// TestChecker_EmptyCallerDenied pins the L3 fail-closed contract:
// a Check with no Caller (nil context or empty Caller field) is
// denied even when a permission matches no groups. The HTTP boundary
// is responsible for populating Caller from a verified identity; the
// absence of one IS the absence of verified authentication, which
// must not be allowed to act.
func TestChecker_EmptyCallerDenied(t *testing.T) {
	checker := NewChecker(createTestNetworkSpec())

	t.Run("nil context", func(t *testing.T) {
		if err := checker.Check(PermServiceApply, nil); err == nil {
			t.Error("nil context should be denied — no Caller means no verified identity")
		}
	})

	t.Run("empty Caller", func(t *testing.T) {
		if err := checker.Check(PermServiceApply, NewContext()); err == nil {
			t.Error("empty Caller should be denied — no Caller means no verified identity")
		}
	})
}

func TestChecker_ServiceWithNilPermissions(t *testing.T) {
	network := &spec.NetworkSpecFile{
		SuperUsers: []string{},
		UserGroups: map[string][]string{
			"neteng": {"alice"},
		},
		Permissions: map[string]spec.PermissionGrants{
			"service.apply": shorthand("neteng"),
		},
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{
				"no-perms-service": {
					Description: "Service with nil permissions",
					Permissions: nil, // Explicitly nil
				},
			},
		},
	}
	checker := NewChecker(network)

	// Should fall back to global permissions
	ctx := callerCtx("alice").WithService("no-perms-service")
	if err := checker.Check(PermServiceApply, ctx); err != nil {
		t.Errorf("Should fall back to global permission: %v", err)
	}
}

func TestChecker_GlobalPermissionNotFound(t *testing.T) {
	network := &spec.NetworkSpecFile{
		SuperUsers:  []string{},
		UserGroups:  map[string][]string{},
		Permissions: map[string]spec.PermissionGrants{}, // No permissions defined
	}
	checker := NewChecker(network)

	err := checker.Check(PermServiceApply, callerCtx("anyone"))
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
		Permissions: map[string]spec.PermissionGrants{
			"all": shorthand("admins"), // Only admins have 'all'
		},
	}
	checker := NewChecker(network)

	// normal-user should be denied (not in admins group)
	err := checker.Check(PermServiceApply, callerCtx("normal-user"))
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
		Permissions: map[string]spec.PermissionGrants{},
		OverridableSpecs: spec.OverridableSpecs{
			Services: map[string]*spec.ServiceSpec{
				"restricted": {
					Description: "Restricted service",
					Permissions: map[string]spec.PermissionGrants{
						"all": shorthand("admins"), // Only admins have 'all' on this service
					},
				},
			},
		},
	}
	checker := NewChecker(network)

	ctx := callerCtx("normal-user").WithService("restricted")
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
		if strings.Contains(msg, "for service") || strings.Contains(msg, "on device") {
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
		if !strings.Contains(msg, "test-svc") {
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
		if !strings.Contains(msg, "leaf1") {
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
		if !strings.Contains(msg, "svc1") || !strings.Contains(msg, "dev1") {
			t.Error("Should mention both service and device")
		}
	})
}
