//go:build e2e

package e2e_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/configlet"
)

// configletDir returns the path to the project's configlets directory.
func configletDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testutil.ProjectRoot(), "configlets")
}

func TestE2E_BaselineListConfiglets(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Baseline", "none")

	dir := configletDir(t)

	names, err := configlet.ListConfiglets(dir)
	if err != nil {
		testutil.TrackComment(t, "configlets dir missing")
		t.Skipf("configlets directory not available: %v", err)
	}

	if len(names) == 0 {
		t.Fatal("expected at least 1 configlet, got 0")
	}

	// Verify known configlets are present
	expected := map[string]bool{
		"sonic-baseline":   false,
		"sonic-evpn":       false,
		"sonic-evpn-leaf":  false,
		"sonic-acl-copp":   false,
		"sonic-qos-8q":     false,
	}

	for _, name := range names {
		t.Logf("  found configlet: %s", name)
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected configlet %q not found", name)
		}
	}
}

func TestE2E_BaselineLoadAndResolve(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Baseline", "leaf")

	dir := configletDir(t)

	c, err := configlet.LoadConfiglet(dir, "sonic-baseline")
	if err != nil {
		testutil.TrackComment(t, "configlet load failed")
		t.Skipf("could not load sonic-baseline configlet: %v", err)
	}

	if c.Name != "sonic-baseline" {
		t.Errorf("configlet name = %q, want %q", c.Name, "sonic-baseline")
	}

	if len(c.Variables) == 0 {
		t.Error("expected configlet to declare variables")
	}

	// Build vars from device profile
	nodeName := leafNodeName(t)
	dev := testutil.LabConnectedDevice(t, nodeName)
	resolved := dev.Resolved()

	vars := map[string]string{
		"device_name":  nodeName,
		"loopback_ip":  resolved.LoopbackIP,
		"hwsku":        "virtual",
		"platform":     "vs-platform",
		"ntp_server_1": "10.0.0.1",
		"ntp_server_2": "10.0.0.2",
		"syslog_server": "10.0.0.3",
		"as_number":    "65000",
	}

	resolvedDB := configlet.ResolveConfiglet(c, vars)

	// Assert resolved entries have correct hostname and loopback IP
	if metadata, ok := resolvedDB["DEVICE_METADATA"]; ok {
		if localhost, ok := metadata["localhost"]; ok {
			if localhost["hostname"] != nodeName {
				t.Errorf("resolved hostname = %q, want %q", localhost["hostname"], nodeName)
			}
		} else {
			t.Error("DEVICE_METADATA|localhost not found in resolved config")
		}
	} else {
		t.Error("DEVICE_METADATA table not found in resolved config")
	}

	if loopback, ok := resolvedDB["LOOPBACK_INTERFACE"]; ok {
		expectedKey := "Loopback0|" + resolved.LoopbackIP + "/32"
		if _, ok := loopback[expectedKey]; !ok {
			t.Errorf("expected LOOPBACK_INTERFACE key %q not found", expectedKey)
		}
	} else {
		t.Error("LOOPBACK_INTERFACE table not found in resolved config")
	}
}

func TestE2E_BaselineApplyAndVerify(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Baseline", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Snapshot original DEVICE_METADATA for restoration
	snapshotClient := testutil.LabRedisClient(t, nodeName, 4)
	originalMeta, err := snapshotClient.HGetAll(context.Background(), "DEVICE_METADATA|localhost").Result()
	if err != nil {
		t.Fatalf("reading DEVICE_METADATA snapshot: %v", err)
	}

	// Snapshot original LOOPBACK_INTERFACE entries
	loKeys, err := snapshotClient.Keys(context.Background(), "LOOPBACK_INTERFACE|*").Result()
	if err != nil {
		t.Fatalf("reading LOOPBACK_INTERFACE keys: %v", err)
	}
	loSnapshot := make(map[string]map[string]string)
	for _, k := range loKeys {
		fields, err := snapshotClient.HGetAll(context.Background(), k).Result()
		if err == nil {
			loSnapshot[k] = fields
		}
	}

	// Cleanup: restore original DEVICE_METADATA and LOOPBACK_INTERFACE
	// This is critical to keep the device in its original lab config for subsequent tests.
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()

		// Restore DEVICE_METADATA
		client.Del(c, "DEVICE_METADATA|localhost")
		if len(originalMeta) > 0 {
			args := make([]interface{}, 0, len(originalMeta)*2)
			for k, v := range originalMeta {
				args = append(args, k, v)
			}
			client.HSet(c, "DEVICE_METADATA|localhost", args...)
		}

		// Restore LOOPBACK_INTERFACE entries: delete any new ones, restore originals
		currentKeys, _ := client.Keys(c, "LOOPBACK_INTERFACE|*").Result()
		for _, k := range currentKeys {
			client.Del(c, k)
		}
		for k, fields := range loSnapshot {
			if len(fields) == 0 {
				// Empty hash — just create the key with a dummy field then delete it,
				// or use HSet with no fields (Redis needs at least one field).
				// SONiC uses empty hashes, so set a NULL field.
				client.HSet(c, k, "NULL", "NULL")
			} else {
				args := make([]interface{}, 0, len(fields)*2)
				for fk, fv := range fields {
					args = append(args, fk, fv)
				}
				client.HSet(c, k, args...)
			}
		}
	})

	// Apply sonic-baseline configlet
	dev := testutil.LabLockedDevice(t, nodeName)
	cs, err := dev.ApplyBaseline(ctx, "sonic-baseline", []string{
		"device_name=" + nodeName,
	})
	if err != nil {
		t.Fatalf("ApplyBaseline: %v", err)
	}
	if err := cs.Apply(dev); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify CONFIG_DB has expected baseline values
	testutil.AssertConfigDBEntry(t, nodeName, "DEVICE_METADATA", "localhost", map[string]string{
		"hostname": nodeName,
	})

	// Verify loopback interface was created (if the device has a loopback IP)
	resolved := dev.Resolved()
	if resolved != nil && resolved.LoopbackIP != "" {
		expectedLo := "Loopback0|" + resolved.LoopbackIP + "/32"
		testutil.AssertConfigDBEntryExists(t, nodeName, "LOOPBACK_INTERFACE", expectedLo)
	}
}
