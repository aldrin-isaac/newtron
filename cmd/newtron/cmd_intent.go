package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/cli"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/util"
	"github.com/spf13/cobra"
)

var intentCmd = &cobra.Command{
	Use:   "intent",
	Short: "Intent DAG operations",
	Long:  `View, inspect, and detect drift from the intent DAG on a device.`,
}

// ============================================================================
// intent tree — DAG tree view
// ============================================================================

var intentAncestors bool

var intentTreeCmd = &cobra.Command{
	Use:   "tree [resource-kind[:<resource>]]",
	Short: "Display the intent DAG as a tree",
	Long: `Display the intent DAG as a tree, rooted at the device or scoped to a
specific resource kind or resource.

Forms:
  newtron switch1 intent tree                    # full tree from device root
  newtron switch1 intent tree vlan               # all VLAN subtrees
  newtron switch1 intent tree vlan:100            # specific VLAN subtree
  newtron switch1 intent tree interface:Ethernet0 # specific interface subtree
  newtron switch1 intent tree vlan:100 --ancestors # include path to root`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		var kind, resource string
		if len(args) == 1 {
			parts := strings.SplitN(args[0], ":", 2)
			kind = parts[0]
			if len(parts) == 2 {
				resource = parts[1]
			}
		}

		nodes, err := app.client.IntentTree(app.deviceName, kind, resource, intentAncestors)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(nodes)
		}

		if len(nodes) == 0 {
			fmt.Println("No intent records found.")
			return nil
		}

		for i, node := range nodes {
			if i > 0 {
				fmt.Println()
			}
			printIntentTree(node, "", true)
		}
		return nil
	},
}

// ============================================================================
// intent drift — CONFIG_DB drift from intent or topology
// ============================================================================

var intentDriftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Detect CONFIG_DB drift from expected state",
	Long: `Reconstruct expected CONFIG_DB and compare against actual CONFIG_DB.
Reports missing, extra, and modified entries in newtron-owned tables.

Without --topology: uses device NEWTRON_INTENT records as expected state.
With --topology: uses topology.json steps as expected state.

Requires -D (device) flag and a connected device.

Examples:
  newtron leaf1 intent drift
  newtron leaf1 --topology intent drift
  newtron leaf1 intent drift --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		mode := intentMode()
		entries, err := app.client.IntentDrift(app.deviceName, mode)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(entries)
		}

		label := app.deviceName
		if mode == "topology" {
			label += " (topology)"
		}

		if len(entries) == 0 {
			fmt.Printf("\nDrift Report for %s: %s\n", bold(label), green("CLEAN"))
			fmt.Println("No drift detected in newtron-owned tables.")
			return nil
		}

		fmt.Printf("\nDrift Report for %s: %s\n", bold(label), red("DRIFTED"))
		printDriftEntries(entries)
		return nil
	},
}

// ============================================================================
// intent reconcile — deliver projection to device to eliminate drift
// ============================================================================

var (
	reconcileFull  bool
	reconcileDelta bool
)

var intentReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Deliver expected state to device, eliminating drift",
	Long: `Reconstruct expected CONFIG_DB and apply it to the device.

Two delivery modes:
  --full   Config reload + full ReplaceAll (default for topology mode)
  --delta  Patch only drifted entries, no reload (default for actuated mode)

Without --topology: reconciles from device NEWTRON_INTENT records.
With --topology: reconciles from topology.json steps.

Dry-run by default. Use -x to execute.

Examples:
  newtron leaf1 intent reconcile -x                      # delta (default, actuated)
  newtron leaf1 intent reconcile --full -x               # full (actuated)
  newtron leaf1 --topology intent reconcile -x           # full (default, topology)
  newtron leaf1 --topology intent reconcile --delta -x   # delta (topology)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		if reconcileFull && reconcileDelta {
			return fmt.Errorf("--full and --delta are mutually exclusive")
		}

		mode := intentMode()
		reconcileMode := reconcileModeFromFlags(mode)

		result, err := app.client.Reconcile(app.deviceName, mode, reconcileMode, execOpts())
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(result)
		}

		if result.Applied == 0 {
			fmt.Printf("Reconcile (%s) for %s: %s\n", result.Mode, bold(app.deviceName), green("no changes needed"))
		} else {
			fmt.Printf("Reconcile (%s) for %s: %d entries applied", result.Mode, bold(app.deviceName), result.Applied)
			if result.Mode == "delta" {
				fmt.Printf(" (missing: %d, extra: %d, modified: %d)", result.Missing, result.Extra, result.Modified)
			}
			fmt.Println()
		}
		if !app.executeMode {
			printDryRunNotice()
		}
		return nil
	},
}

// reconcileModeFromFlags returns the reconcile mode from CLI flags.
// Empty string means "use server default" (topology→full, actuated→delta).
func reconcileModeFromFlags(intentSource string) string {
	if reconcileFull {
		return "full"
	}
	if reconcileDelta {
		return "delta"
	}
	return "" // let server pick default
}

// ============================================================================
// intent save — persist device intent DB to topology.json
// ============================================================================

var intentSaveCmd = &cobra.Command{
	Use:   "save",
	Short: "Persist device intent DB back to topology.json",
	Long: `Persist the device's current NEWTRON_INTENT records back to topology.json.

With --topology: writes topology-format steps.
Without --topology: saves in intent format.

Examples:
  newtron leaf1 intent save
  newtron leaf1 --topology intent save`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		mode := intentMode()
		snap, err := app.client.IntentSave(app.deviceName, mode)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(snap)
		}

		fmt.Printf("Saved %d steps for %s\n", len(snap.Steps), bold(app.deviceName))
		return nil
	},
}

// ============================================================================
// intent reload — rebuild node from topology.json (topology mode only)
// ============================================================================

var intentReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Rebuild node intent from topology.json",
	Long: `Rebuild the node's intent DAG from topology.json steps.
Topology mode only (implicitly uses --topology).

Examples:
  newtron leaf1 intent reload
  newtron leaf1 intent reload --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		snap, err := app.client.IntentReload(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(snap)
		}

		fmt.Printf("Reloaded %d steps for %s\n", len(snap.Steps), bold(app.deviceName))
		return nil
	},
}

// ============================================================================
// intent clear — reset node to ports-only state (topology mode only)
// ============================================================================

var intentClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Reset node to ports-only state",
	Long: `Reset the node's intent DAG to an empty state with ports only.
Topology mode only (implicitly uses --topology).

Examples:
  newtron leaf1 intent clear`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		snap, err := app.client.IntentClear(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(snap)
		}

		fmt.Printf("Cleared intent DAG for %s (%d steps remain)\n", bold(app.deviceName), len(snap.Steps))
		return nil
	},
}

// ============================================================================
// Shared display helpers
// ============================================================================

// intentMode returns "topology" if the --topology flag is set, otherwise "".
func intentMode() string {
	if app.topology {
		return "topology"
	}
	return ""
}

func printDriftEntries(entries []newtron.DriftEntry) {
	var missing, extra, modified []newtron.DriftEntry
	for _, e := range entries {
		switch e.Type {
		case "missing":
			missing = append(missing, e)
		case "extra":
			extra = append(extra, e)
		case "modified":
			modified = append(modified, e)
		}
	}

	if len(missing) > 0 {
		fmt.Printf("\n%s (%d):\n", red("Missing entries"), len(missing))
		t := cli.NewTable("TABLE", "KEY", "EXPECTED FIELDS")
		for _, d := range missing {
			t.Row(d.Table, d.Key, formatFields(d.Expected))
		}
		t.Flush()
	}

	if len(extra) > 0 {
		fmt.Printf("\n%s (%d):\n", yellow("Extra entries"), len(extra))
		t := cli.NewTable("TABLE", "KEY", "ACTUAL FIELDS")
		for _, d := range extra {
			t.Row(d.Table, d.Key, formatFields(d.Actual))
		}
		t.Flush()
	}

	if len(modified) > 0 {
		fmt.Printf("\n%s (%d):\n", yellow("Modified entries"), len(modified))
		t := cli.NewTable("TABLE", "KEY", "FIELD", "EXPECTED", "ACTUAL")
		for _, d := range modified {
			for field, expectedVal := range d.Expected {
				actualVal := d.Actual[field]
				if expectedVal != actualVal {
					t.Row(d.Table, d.Key, field, expectedVal, actualVal)
				}
			}
			for field, actualVal := range d.Actual {
				if _, ok := d.Expected[field]; !ok {
					t.Row(d.Table, d.Key, field, "(none)", actualVal)
				}
			}
		}
		t.Flush()
	}
}

func formatFields(fields map[string]string) string {
	if len(fields) == 0 {
		return "(empty)"
	}
	parts := make([]string, 0, len(fields))
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return fmt.Sprintf("%v", parts)
}

// printIntentTree recursively renders an IntentTreeNode as a tree.
func printIntentTree(node newtron.IntentTreeNode, prefix string, isRoot bool) {
	line := fmt.Sprintf("%s (%s)", node.Resource, node.Operation)
	if params := formatIntentParams(node.Params); params != "" {
		line += " " + params
	}
	fmt.Println(line)

	if node.Leaf {
		return
	}

	for i, child := range node.Children {
		isLast := i == len(node.Children)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		childPrefix := prefix + "│   "
		if isLast {
			childPrefix = prefix + "    "
		}

		fmt.Print(prefix + connector)
		printIntentTree(child, childPrefix, false)
	}
}

// formatIntentParams formats params as key=value pairs for display.
func formatIntentParams(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, " ")
}

// ============================================================================
// intent projection — expected CONFIG_DB from intent replay
// ============================================================================

var intentProjectionCmd = &cobra.Command{
	Use:   "projection",
	Short: "Show the projection (expected CONFIG_DB from intent replay)",
	Long: `Display the per-Node projection — the per-table per-key per-field
expected state derived from intent replay. This is the substrate representing
"what newtron believes this device should look like." Compare against the
device-reality snapshot ('configdb snapshot') to see drift.

Without --topology / --loopback: reads from device NEWTRON_INTENT replay
(requires a live device).
With --topology: builds from topology.json replay (no device).
With --loopback: reuses the cached topology-built projection for offline
config testing.

Examples:
  newtron leaf1 intent projection
  newtron leaf1 --topology intent projection
  newtron leaf1 --loopback intent projection --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		raw, err := app.client.IntentProjection(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(raw)
		}
		printRawConfigDB(raw)
		return nil
	},
}

// printRawConfigDB renders a sonic.RawConfigDB as a per-table TABLE / KEY /
// FIELDS listing in deterministic order. Used by both 'intent projection'
// and 'configdb snapshot'.
func printRawConfigDB(raw map[string]map[string]map[string]string) {
	if len(raw) == 0 {
		fmt.Println("(empty projection)")
		return
	}
	tables := make([]string, 0, len(raw))
	for t := range raw {
		tables = append(tables, t)
	}
	sort.Strings(tables)

	for _, table := range tables {
		entries := raw[table]
		fmt.Printf("\n%s\n", bold(table))
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields := entries[key]
			fnames := make([]string, 0, len(fields))
			for f := range fields {
				fnames = append(fnames, f)
			}
			sort.Strings(fnames)
			parts := make([]string, 0, len(fnames))
			for _, f := range fnames {
				parts = append(parts, f+"="+fields[f])
			}
			fmt.Printf("  %s: %s\n", key, strings.Join(parts, " "))
		}
	}
}

// intent snapshot — canonical NEWTRON_INTENT dump for before/after comparison
var intentSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Show the device's NEWTRON_INTENT records in canonical form",
	Long: `Dump the device's NEWTRON_INTENT table with DAG links normalized —
the substrate for "is the device back where it started?" comparisons. Unlike
'intent tree' it includes every record (no side-effect/orphan filtering), and
unlike 'intent drift' it does not exclude NEWTRON_INTENT, so a residual or
orphaned intent record is visible here.

Requires -D (device) flag.

Examples:
  newtron leaf1 intent snapshot
  newtron leaf1 intent snapshot --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		snap, err := app.client.IntentSnapshot(app.deviceName)
		if err != nil {
			return err
		}
		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(snap)
		}
		if len(snap) == 0 {
			fmt.Println("(no intent records)")
			return nil
		}
		keys := make([]string, 0, len(snap))
		for k := range snap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields := snap[key]
			fnames := make([]string, 0, len(fields))
			for f := range fields {
				fnames = append(fnames, f)
			}
			sort.Strings(fnames)
			parts := make([]string, 0, len(fnames))
			for _, f := range fnames {
				parts = append(parts, f+"="+fields[f])
			}
			fmt.Printf("%s: %s\n", key, strings.Join(parts, " "))
		}
		return nil
	},
}

// intent snapshot-diff — compare the device's current intent DB to a saved baseline
var intentSnapshotDiffCmd = &cobra.Command{
	Use:   "snapshot-diff <baseline.json>",
	Short: "Diff the device's current NEWTRON_INTENT against a saved snapshot",
	Long: `Compare the device's current intent DB to a baseline captured earlier
with 'intent snapshot --json > baseline.json'. Reports records that are residual
(present now, not in the baseline), missing (in the baseline, gone now), or
changed. Exits non-zero when they diverge, so it composes as a before/after gate.

Requires -D (device) flag.

Examples:
  newtron leaf1 intent snapshot --json > baseline.json
  newtron leaf1 intent snapshot-diff baseline.json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("reading baseline %q: %w", args[0], err)
		}
		var baseline util.IntentRecords
		if err := json.Unmarshal(data, &baseline); err != nil {
			return fmt.Errorf("parsing baseline %q: %w", args[0], err)
		}
		current, err := app.client.IntentSnapshot(app.deviceName)
		if err != nil {
			return err
		}
		diff := util.DiffIntentRecords(baseline, current)
		fmt.Println(diff.Summary(args[0]))
		if !diff.Empty() {
			return fmt.Errorf("intent DB diverged from %s", args[0])
		}
		return nil
	},
}

func init() {
	// intent tree
	intentTreeCmd.Flags().BoolVar(&intentAncestors, "ancestors", false, "Show path from resource to root")

	// intent reconcile flags
	intentReconcileCmd.Flags().BoolVar(&reconcileFull, "full", false, "Full reconcile (config reload + ReplaceAll)")
	intentReconcileCmd.Flags().BoolVar(&reconcileDelta, "delta", false, "Delta reconcile (patch only drifted entries)")

	// Register -x/--execute on intentReconcileCmd — the only intent
	// subcommand that branches on app.executeMode. Issue #62 surfaced
	// the gap: newtlab's provisioning step invokes
	// `newtron <dev> --topology intent reconcile -x` and got
	// "unknown shorthand flag" — the variable was declared globally on
	// `app` but never bound to this subcommand.
	addWriteFlags(intentReconcileCmd)

	// Register all under intentCmd
	intentCmd.AddCommand(intentTreeCmd)
	intentCmd.AddCommand(intentProjectionCmd)
	intentCmd.AddCommand(intentSnapshotCmd)
	intentCmd.AddCommand(intentSnapshotDiffCmd)
	intentCmd.AddCommand(intentDriftCmd)
	intentCmd.AddCommand(intentReconcileCmd)
	intentCmd.AddCommand(intentSaveCmd)
	intentCmd.AddCommand(intentReloadCmd)
	intentCmd.AddCommand(intentClearCmd)
}
