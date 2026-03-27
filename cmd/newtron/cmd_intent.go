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
// intent list — raw intent records
// ============================================================================

var intentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List intent records on the device",
	Long: `Show all NEWTRON_INTENT records on the device. Each record tracks
a service binding or device-level operation that newtron applied.

Requires -D (device) flag. No lock required (read-only query).

Examples:
  newtron leaf1 intent list
  newtron leaf1 intent list --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		intents, err := app.client.ListIntents(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(intents)
		}

		if len(intents) == 0 {
			fmt.Println("No intents.")
			return nil
		}

		for _, intent := range intents {
			fmt.Printf("%-16s  %-15s  %-20s  %s\n",
				intent.Resource, intent.State, intent.Operation, intent.Name)
		}
		return nil
	},
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
// intent drift — CONFIG_DB drift from intent records
// ============================================================================

var intentDriftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Detect CONFIG_DB drift from expected state",
	Long: `Reconstruct expected CONFIG_DB from the device's NEWTRON_INTENT records
and compare against actual CONFIG_DB. Reports missing, extra, and modified
entries in newtron-owned tables.

Requires -D (device) flag and a connected device.

Examples:
  newtron leaf1 intent drift
  newtron leaf1 intent drift --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		report, err := app.client.DetectDrift(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(report)
		}

		fmt.Printf("\nDrift Report for %s: %s\n", bold(app.deviceName), formatDriftStatus(report.Status))

		if report.Status == "clean" {
			fmt.Println("No drift detected in newtron-owned tables.")
			return nil
		}

		printDriftEntries(report)
		return nil
	},
}

// ============================================================================
// intent topology — topology-based drift and snapshot
// ============================================================================

var intentTopologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Topology-related intent operations",
}

var intentTopologyDriftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Detect CONFIG_DB drift from topology-defined state",
	Long: `Compare expected CONFIG_DB (from topology steps) against actual CONFIG_DB
on the device. Reports operations applied outside the topology.

Unlike 'intent drift' (intent-based), this compares against what the topology
file says should be configured, not what the device's own intents say.

Requires -D (device) flag and a loaded topology.

Examples:
  newtron leaf1 intent topology drift
  newtron leaf1 intent topology drift --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		report, err := app.client.DetectTopologyDrift(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(report)
		}

		fmt.Printf("\nTopology Drift Report for %s: %s\n", bold(app.deviceName), formatDriftStatus(report.Status))

		if report.Status == "clean" {
			fmt.Println("Device CONFIG_DB matches topology-defined state.")
			return nil
		}

		printDriftEntries(report)
		return nil
	},
}

var intentTopologyIntentsCmd = &cobra.Command{
	Use:   "intents",
	Short: "Show device intents projected as topology steps",
	Long: `Project the device's actuated NEWTRON_INTENT records back into topology
step format. Shows what topology steps would reproduce the device's current state.

Requires -D (device) flag and a connected device.

Examples:
  newtron leaf1 intent topology intents
  newtron leaf1 intent topology intents --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		snap, err := app.client.TopologyIntents(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(snap)
		}

		fmt.Printf("\nTopology Intents for %s (%d steps)\n\n", bold(app.deviceName), len(snap.Steps))
		if len(snap.Steps) == 0 {
			fmt.Println("No actuated intents on this device.")
			return nil
		}

		t := cli.NewTable("#", "URL", "PARAMS")
		for i, step := range snap.Steps {
			params := ""
			if len(step.Params) > 0 {
				parts := make([]string, 0, len(step.Params))
				for k, v := range step.Params {
					parts = append(parts, fmt.Sprintf("%s=%v", k, v))
				}
				params = fmt.Sprintf("%v", parts)
			}
			t.Row(fmt.Sprintf("%d", i+1), step.URL, params)
		}
		t.Flush()
		return nil
	},
}

// ============================================================================
// Shared display helpers
// ============================================================================

func formatDriftStatus(status string) string {
	switch status {
	case "clean":
		return green("CLEAN")
	case "drifted":
		return red("DRIFTED")
	default:
		return status
	}
}

func printDriftEntries(report *newtron.DriftReport) {
	if len(report.Missing) > 0 {
		fmt.Printf("\n%s (%d):\n", red("Missing entries"), len(report.Missing))
		t := cli.NewTable("TABLE", "KEY", "EXPECTED FIELDS")
		for _, d := range report.Missing {
			t.Row(d.Table, d.Key, formatFields(d.Expected))
		}
		t.Flush()
	}

	if len(report.Extra) > 0 {
		fmt.Printf("\n%s (%d):\n", yellow("Extra entries"), len(report.Extra))
		t := cli.NewTable("TABLE", "KEY", "ACTUAL FIELDS")
		for _, d := range report.Extra {
			t.Row(d.Table, d.Key, formatFields(d.Actual))
		}
		t.Flush()
	}

	if len(report.Modified) > 0 {
		fmt.Printf("\n%s (%d):\n", yellow("Modified entries"), len(report.Modified))
		t := cli.NewTable("TABLE", "KEY", "FIELD", "EXPECTED", "ACTUAL")
		for _, d := range report.Modified {
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

	// intent topology subcommands
	intentTopologyCmd.AddCommand(intentTopologyDriftCmd)
	intentTopologyCmd.AddCommand(intentTopologyIntentsCmd)

	// Register all under intentCmd
	intentCmd.AddCommand(intentListCmd)
	intentCmd.AddCommand(intentTreeCmd)
	intentCmd.AddCommand(intentDriftCmd)
	intentCmd.AddCommand(intentTopologyCmd)
}
