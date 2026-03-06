package main

import (
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

func init() {
	deviceCleanupCmd.Flags().StringVar(&cleanupType, "type", "", "Cleanup specific type only (acls, vrfs, vnis)")

	deviceCmd.AddCommand(deviceCleanupCmd)
}
