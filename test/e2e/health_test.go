//go:build e2e

package e2e_test

import (
	"context"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/health"
	"github.com/newtron-network/newtron/pkg/operations"
)

func TestE2E_HealthCheckAll(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Health", "all")

	nodes := testutil.LabSonicNodes(t)
	if len(nodes) == 0 {
		t.Skip("no SONiC nodes available")
	}

	for _, node := range nodes {
		t.Run(node.Name, func(t *testing.T) {
			ctx := testutil.LabContext(t)
			dev := testutil.LabConnectedDevice(t, node.Name)

			results, err := dev.RunHealthChecks(ctx, "")
			if err != nil {
				t.Fatalf("RunHealthChecks: %v", err)
			}

			if len(results) == 0 {
				t.Fatal("expected at least 1 health check result, got 0")
			}

			for _, r := range results {
				t.Logf("  %s: %s — %s", r.Check, r.Status, r.Message)
				if r.Status == "fail" {
					t.Errorf("health check %q failed: %s", r.Check, r.Message)
				}
			}
		})
	}
}

func TestE2E_HealthCheckByType(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Health", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)
	dev := testutil.LabConnectedDevice(t, nodeName)

	checkTypes := []string{"interfaces", "bgp", "evpn", "lag"}

	for _, ct := range checkTypes {
		t.Run(ct, func(t *testing.T) {
			results, err := dev.RunHealthChecks(ctx, ct)
			if err != nil {
				t.Fatalf("RunHealthChecks(%q): %v", ct, err)
			}

			if len(results) != 1 {
				t.Fatalf("expected exactly 1 result for check type %q, got %d", ct, len(results))
			}

			r := results[0]
			if r.Check != ct {
				t.Errorf("result check name = %q, want %q", r.Check, ct)
			}
			t.Logf("  %s: %s — %s", r.Check, r.Status, r.Message)
		})
	}
}

func TestE2E_HealthCheckerPackage(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Health", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)
	dev := testutil.LabConnectedDevice(t, nodeName)

	checker := health.NewChecker()
	report, err := checker.Run(ctx, dev)
	if err != nil {
		t.Fatalf("checker.Run: %v", err)
	}

	if report.Device != nodeName {
		t.Errorf("report.Device = %q, want %q", report.Device, nodeName)
	}

	if report.Overall == health.StatusCritical {
		t.Errorf("report.Overall = %q, expected non-critical", report.Overall)
	}

	expectedChecks := len(checker.ListChecks())
	if len(report.Results) != expectedChecks {
		t.Errorf("report.Results has %d entries, want %d", len(report.Results), expectedChecks)
	}

	if report.Duration <= 0 {
		t.Errorf("report.Duration = %v, expected > 0", report.Duration)
	}

	t.Logf("  overall: %s, checks: %d, duration: %v", report.Overall, len(report.Results), report.Duration)
	for _, r := range report.Results {
		t.Logf("    %s: %s — %s (%v)", r.Check, r.Status, r.Message, r.Duration)
	}
}

func TestE2E_HealthCheckAfterConfigChange(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Health", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Cleanup: delete VLAN 510 via Redis (registered first so it runs last)
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		client.Del(context.Background(), "VLAN|Vlan510")
	})

	// Create a VLAN to introduce a transient config change
	dev := testutil.LabLockedDevice(t, nodeName)
	op := &operations.CreateVLANOp{ID: 510, Desc: "e2e-health-test"}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VLAN: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VLAN: %v", err)
	}

	// Run health checks — should not fail due to transient VLAN
	checkDev := testutil.LabConnectedDevice(t, nodeName)
	results, err := checkDev.RunHealthChecks(ctx, "")
	if err != nil {
		t.Fatalf("RunHealthChecks after create: %v", err)
	}
	for _, r := range results {
		if r.Status == "fail" {
			t.Errorf("health check %q failed after VLAN create: %s", r.Check, r.Message)
		}
	}

	// Delete VLAN 510 via operations
	delDev := testutil.LabLockedDevice(t, nodeName)
	delOp := &operations.DeleteVLANOp{ID: 510}
	if err := delOp.Validate(ctx, delDev); err != nil {
		t.Fatalf("validate delete VLAN: %v", err)
	}
	if err := delOp.Execute(ctx, delDev); err != nil {
		t.Fatalf("execute delete VLAN: %v", err)
	}

	// Run health checks again — should still pass
	checkDev2 := testutil.LabConnectedDevice(t, nodeName)
	results2, err := checkDev2.RunHealthChecks(ctx, "")
	if err != nil {
		t.Fatalf("RunHealthChecks after delete: %v", err)
	}
	for _, r := range results2 {
		if r.Status == "fail" {
			t.Errorf("health check %q failed after VLAN delete: %s", r.Check, r.Message)
		}
	}
}
