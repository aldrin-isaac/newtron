package newtrun

import (
	"reflect"
	"testing"
)

// buildCLIArgs is the pure argv-build helper that runCLI delegates
// to. Tests pin the wire shape directly — no exec mocking, no
// process spawn.

// TestBuildCLIArgs_ForwardsNetworkID is the regression test for the
// gap that motivated this fix (#247). When the Runner is on a
// per-suite network id, the subprocess CLI must hit the same slot;
// without --network-id forwarding the subprocess falls back to its
// own resolution and "device not found in topology" surfaces.
func TestBuildCLIArgs_ForwardsNetworkID(t *testing.T) {
	r := &Runner{
		ServerURL: "http://srv:18080",
		NetworkID: "1node-vs",
	}
	step := &Step{Command: "device setup --hostname switch1"}

	got := buildCLIArgs(r, step, "switch1")
	want := []string{"switch1", "device", "setup", "--hostname", "switch1", "--network-id", "1node-vs", "--server", "http://srv:18080"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

// TestBuildCLIArgs_EmptyNetworkID_Omitted confirms the forward is
// scoped — when the Runner has no NetworkID the flag is absent
// (subprocess falls back to its own resolution, which is the right
// behavior when the runner isn't network-scoped).
func TestBuildCLIArgs_EmptyNetworkID_Omitted(t *testing.T) {
	r := &Runner{ServerURL: "http://srv:18080"}
	step := &Step{Command: "vlan list"}

	got := buildCLIArgs(r, step, "")
	for i, a := range got {
		if a == "--network-id" {
			t.Errorf("argv[%d] = %q; --network-id should be omitted when NetworkID is empty (full argv: %v)", i, a, got)
		}
	}
}

// TestBuildCLIArgs_JQAddsJSON pins the existing behavior: when an
// expect.jq is present, --json is added so the CLI emits parseable
// output. Worth pinning since the new --network-id insertion
// happens nearby and could regress this.
func TestBuildCLIArgs_JQAddsJSON(t *testing.T) {
	r := &Runner{ServerURL: "http://srv:18080", NetworkID: "demo"}
	step := &Step{
		Command: "vlan list",
		Expect:  &ExpectBlock{JQ: ".[0].vlan_id == 100"},
	}

	got := buildCLIArgs(r, step, "leaf1")
	want := []string{"leaf1", "vlan", "list", "--json", "--network-id", "demo", "--server", "http://srv:18080"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

// TestBuildCLIArgs_DeviceTemplateExpansion pins the {{device}}
// substitution that runCLI relies on.
func TestBuildCLIArgs_DeviceTemplateExpansion(t *testing.T) {
	r := &Runner{ServerURL: "http://srv:18080"}
	step := &Step{Command: "configdb query DEVICE_METADATA {{device}}"}

	got := buildCLIArgs(r, step, "switch1")
	want := []string{"switch1", "configdb", "query", "DEVICE_METADATA", "switch1", "--server", "http://srv:18080"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}
