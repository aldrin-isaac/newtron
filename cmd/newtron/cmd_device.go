package main

import (
	"fmt"
	"time"

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

var deviceZombieCmd = &cobra.Command{
	Use:   "zombie",
	Short: "Show zombie operation from a crashed process",
	Long: `Show details of a zombie operation left by a crashed process.

A zombie operation indicates that a previous newtron process died mid-write,
leaving the device in an unknown partial state. New operations are blocked
until the zombie is resolved via 'zombie rollback' or 'zombie clear'.

Requires -D (device) flag. No lock required (read-only STATE_DB query).

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
	Long: `Delete the zombie operation record from STATE_DB without reversing
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

func init() {
	deviceCleanupCmd.Flags().StringVar(&cleanupType, "type", "", "Cleanup specific type only (acls, vrfs, vnis)")

	deviceZombieCmd.AddCommand(deviceZombieRollbackCmd)
	deviceZombieCmd.AddCommand(deviceZombieClearCmd)

	deviceCmd.AddCommand(deviceCleanupCmd)
	deviceCmd.AddCommand(deviceZombieCmd)
}
