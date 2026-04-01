package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
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

var intentReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Deliver expected state to device, eliminating drift",
	Long: `Reconstruct expected CONFIG_DB and apply it to the device.

Without --topology: reconciles from device NEWTRON_INTENT records.
With --topology: reconciles from topology.json steps.

Dry-run by default. Use -x to execute.

Examples:
  newtron leaf1 intent reconcile           # dry-run (shows drift)
  newtron leaf1 intent reconcile -x        # execute
  newtron leaf1 --topology intent reconcile -x  # topology mode`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		mode := intentMode()
		result, err := app.client.Reconcile(app.deviceName, mode, execOpts())
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(result)
		}

		if result.Applied == 0 {
			fmt.Printf("Reconcile for %s: %s\n", bold(app.deviceName), green("no changes needed"))
		} else {
			fmt.Printf("Reconcile for %s: %d entries applied\n", bold(app.deviceName), result.Applied)
		}
		if !app.executeMode {
			printDryRunNotice()
		}
		return nil
	},
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

func init() {
	// intent tree
	intentTreeCmd.Flags().BoolVar(&intentAncestors, "ancestors", false, "Show path from resource to root")

	// Register all under intentCmd
	intentCmd.AddCommand(intentTreeCmd)
	intentCmd.AddCommand(intentDriftCmd)
	intentCmd.AddCommand(intentReconcileCmd)
	intentCmd.AddCommand(intentSaveCmd)
	intentCmd.AddCommand(intentReloadCmd)
	intentCmd.AddCommand(intentClearCmd)
}
