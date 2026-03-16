package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/spf13/cobra"
)

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Device-scope operations",
	Long: `Device-scope operations that don't fit a specific resource group.

Examples:
  newtron -D leaf1-ny device cleanup -x
  newtron -D leaf1-ny device cleanup --type acls -x`,
}

var cleanupType string

var deviceCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove orphaned configurations from the device",
	Long: `Remove orphaned configurations from the device.

This command identifies and removes configurations that are no longer in use:
  - ACL tables not bound to any interface
  - VRFs with no interface bindings
  - VNI mappings for deleted VLANs/VRFs
  - Unused EVPN route targets

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny device cleanup
  newtron -D leaf1-ny device cleanup -x
  newtron -D leaf1-ny device cleanup --type acls -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		return displayWriteResult(app.client.Cleanup(app.deviceName, cleanupType, execOpts()))
	},
}

var deviceIntentsCmd = &cobra.Command{
	Use:   "intents",
	Short: "List intent records on the device",
	Long: `Show all NEWTRON_INTENT records on the device. Each record tracks
a service binding or device-level operation that newtron applied.

Requires -D (device) flag. No lock required (read-only query).

Examples:
  newtron -D leaf1 device intents
  newtron -D leaf1 device intents --json`,
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

var deviceZombieCmd = &cobra.Command{
	Use:   "zombie",
	Short: "Show zombie operation from a crashed process",
	Long: `Show details of a zombie operation left by a crashed process.

A zombie operation indicates that a previous newtron process died mid-write,
leaving the device in an unknown partial state. New operations are blocked
until the zombie is resolved via 'zombie rollback' or 'zombie clear'.

Requires -D (device) flag. No lock required (read-only CONFIG_DB query).

Examples:
  newtron -D leaf1 device zombie`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		intent, err := app.client.ReadZombie(app.deviceName)
		if err != nil {
			return err
		}
		if intent == nil || intent.Holder == "" {
			fmt.Println("No zombie operation.")
			return nil
		}

		fmt.Printf("Zombie operation on %s\n", app.deviceName)
		fmt.Printf("  Holder:  %s\n", intent.Holder)
		fmt.Printf("  Created: %s\n", intent.Created.Format(time.RFC3339))
		if intent.Phase != "" {
			fmt.Printf("  Phase:   %s\n", intent.Phase)
		}
		if intent.RollbackHolder != "" {
			fmt.Printf("  Rollback by: %s\n", intent.RollbackHolder)
		}
		if intent.RollbackStarted != nil {
			fmt.Printf("  Rollback started: %s\n", intent.RollbackStarted.Format(time.RFC3339))
		}
		fmt.Printf("  Operations (%d):\n", len(intent.Operations))
		for i, op := range intent.Operations {
			status := "not started"
			if op.Reversed != nil {
				status = "reversed"
			} else if op.Started != nil && op.Completed != nil {
				status = "completed"
			} else if op.Started != nil {
				status = "in progress (partial)"
			}
			reverse := "none"
			if op.ReverseOp != "" {
				reverse = op.ReverseOp
			}
			fmt.Printf("    %d. %s [%s] reverse=%s\n", i+1, op.Name, status, reverse)
			for k, v := range op.Params {
				fmt.Printf("       %s: %s\n", k, v)
			}
		}
		fmt.Println("\nResolve with:")
		fmt.Printf("  newtron -D %s device zombie rollback     # preview rollback\n", app.deviceName)
		fmt.Printf("  newtron -D %s device zombie rollback -x  # execute rollback\n", app.deviceName)
		fmt.Printf("  newtron -D %s device zombie clear        # dismiss without rollback\n", app.deviceName)
		return nil
	},
}

var deviceZombieRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Reverse a zombie operation's changes",
	Long: `Reverse a zombie operation's changes to restore the device to its
last known-good state. Calls the reverse of each operation in reverse order.

Acquires a fresh lock on the device. Operations that completed fully get
a full reverse; operations that were in progress get a partial reverse
(existence-checking); operations that never started are skipped.

Requires -D (device) flag. Use -x to execute (default is dry-run preview).

Examples:
  newtron -D leaf1 device zombie rollback     # preview
  newtron -D leaf1 device zombie rollback -x  # execute`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		return displayWriteResult(app.client.RollbackZombie(app.deviceName, execOpts()))
	},
}

var deviceZombieClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Dismiss zombie operation without rollback",
	Long: `Delete the zombie operation record from CONFIG_DB without reversing
any changes. Use when the operator has manually cleaned up the partial
state, or the partial state is acceptable.

Acquires a fresh lock on the device.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 device zombie clear`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		if err := app.client.ClearZombie(app.deviceName); err != nil {
			return err
		}
		fmt.Println("Zombie operation cleared.")
		return nil
	},
}

// ============================================================================
// History commands
// ============================================================================

var deviceHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show rolling operation history",
	Long: `Show the rolling history of committed operations on this device.
Newtron keeps the last 10 operations in CONFIG_DB for rollback.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 device history
  newtron -D leaf1 device history --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		result, err := app.client.ReadHistory(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(result)
		}

		if len(result.Entries) == 0 {
			fmt.Println("No operation history.")
			return nil
		}

		fmt.Printf("\nOperation History for %s (%d entries)\n\n", bold(app.deviceName), len(result.Entries))

		t := cli.NewTable("SEQ", "HOLDER", "TIMESTAMP", "OPS", "STATUS")
		for _, e := range result.Entries {
			status := "active"
			reversed := 0
			for _, op := range e.Operations {
				if op.Reversed != nil {
					reversed++
				}
			}
			if reversed == len(e.Operations) && len(e.Operations) > 0 {
				status = "reversed"
			} else if reversed > 0 {
				status = fmt.Sprintf("partial (%d/%d reversed)", reversed, len(e.Operations))
			}
			t.Row(
				fmt.Sprintf("%d", e.Sequence),
				e.Holder,
				e.Timestamp.Format(time.RFC3339),
				fmt.Sprintf("%d", len(e.Operations)),
				status,
			)
		}
		t.Flush()

		return nil
	},
}

var deviceHistoryRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Reverse the most recent history entry",
	Long: `Reverse the most recent un-reversed history entry.
Uses domain-level reverse operations (not mechanical ChangeSet reversal).

Requires -D (device) flag. Use -x to execute (default is dry-run preview).

Examples:
  newtron -D leaf1 device history rollback     # preview
  newtron -D leaf1 device history rollback -x  # execute`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		result, err := app.client.RollbackHistory(app.deviceName, execOpts())
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(result)
		}

		if result.RolledBack == nil {
			fmt.Println("No history entries to roll back.")
			return nil
		}
		fmt.Printf("Rolled back history entry #%d (%d operations reversed)\n",
			result.RolledBack.Sequence, result.RolledBack.OperationsReversed)
		return nil
	},
}

// ============================================================================
// Drift detection commands
// ============================================================================

var deviceDriftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Detect CONFIG_DB drift from expected state",
	Long: `Compare expected CONFIG_DB (from topology + specs) against actual
CONFIG_DB on the device. Reports missing, extra, and modified entries
in newtron-owned tables.

Requires -D (device) flag and a loaded topology.

Examples:
  newtron -D leaf1 device drift
  newtron -D leaf1 device drift --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		hasTopology, err := app.client.HasTopology()
		if err != nil {
			return err
		}
		if !hasTopology {
			return fmt.Errorf("no topology loaded — drift detection requires a topology")
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
				// Fields in actual not in expected
				for field, actualVal := range d.Actual {
					if _, ok := d.Expected[field]; !ok {
						t.Row(d.Table, d.Key, field, "(none)", actualVal)
					}
				}
			}
			t.Flush()
		}

		return nil
	},
}

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

// ============================================================================
// Settings commands
// ============================================================================

var deviceSettingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show newtron operational settings on the device",
	Long: `Show newtron operational settings stored in CONFIG_DB (NEWTRON_SETTINGS|global).

Settings control per-device operational behavior:
  max_history  Number of rolling history entries to keep (default 10, 0 to disable)

Requires -D (device) flag.

Examples:
  newtron -D leaf1 device settings
  newtron -D leaf1 device settings --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		settings, err := app.client.ReadSettings(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(settings)
		}

		fmt.Printf("\nSettings for %s\n\n", bold(app.deviceName))
		t := cli.NewTable("SETTING", "VALUE")
		t.Row("max_history", fmt.Sprintf("%d", settings.MaxHistory))
		t.Flush()
		return nil
	},
}

var settingsMaxHistory int

var deviceSettingsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Update newtron operational settings",
	Long: `Update newtron operational settings on the device.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 device settings set --max-history 20
  newtron -D leaf1 device settings set --max-history 0   # disable history`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		// Read current settings, apply overrides
		settings, err := app.client.ReadSettings(app.deviceName)
		if err != nil {
			return err
		}

		if cmd.Flags().Changed("max-history") {
			settings.MaxHistory = settingsMaxHistory
		}

		if err := app.client.WriteSettings(app.deviceName, settings); err != nil {
			return err
		}

		fmt.Printf("Settings updated on %s.\n", app.deviceName)
		return nil
	},
}

func init() {
	deviceCleanupCmd.Flags().StringVar(&cleanupType, "type", "", "Cleanup specific type only (acls, vrfs, vnis)")

	deviceZombieCmd.AddCommand(deviceZombieRollbackCmd)
	deviceZombieCmd.AddCommand(deviceZombieClearCmd)

	deviceHistoryCmd.AddCommand(deviceHistoryRollbackCmd)

	deviceSettingsSetCmd.Flags().IntVar(&settingsMaxHistory, "max-history", 10, "Maximum number of history entries (0 to disable)")
	deviceSettingsCmd.AddCommand(deviceSettingsSetCmd)

	deviceCmd.AddCommand(deviceCleanupCmd)
	deviceCmd.AddCommand(deviceIntentsCmd)
	deviceCmd.AddCommand(deviceZombieCmd)
	deviceCmd.AddCommand(deviceHistoryCmd)
	deviceCmd.AddCommand(deviceDriftCmd)
	deviceCmd.AddCommand(deviceSettingsCmd)
}
