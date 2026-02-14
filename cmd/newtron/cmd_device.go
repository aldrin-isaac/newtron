package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/network"
)

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Device-scope operations",
	Long: `Device-scope operations that don't fit a specific resource group.

Examples:
  newtron -d leaf1-ny device cleanup -x
  newtron -d leaf1-ny device cleanup --type acls -x`,
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

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny device cleanup
  newtron -d leaf1-ny device cleanup -x
  newtron -d leaf1-ny device cleanup --type acls -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName)
			if err := checkExecutePermission(auth.PermDeviceCleanup, authCtx); err != nil {
				return nil, err
			}

			cs, summary, err := dev.Cleanup(ctx, cleanupType)
			if err != nil {
				return nil, fmt.Errorf("analyzing orphaned configs: %w", err)
			}

			if cs.IsEmpty() {
				fmt.Println("No orphaned configurations found. Device is clean.")
				return nil, nil
			}

			// Display summary before changeset
			fmt.Printf("Orphaned Configurations on %s\n", bold(app.deviceName))
			fmt.Println(strings.Repeat("=", 50))

			if len(summary.OrphanedACLs) > 0 {
				fmt.Printf("\nOrphaned ACLs (%d):\n", len(summary.OrphanedACLs))
				for _, acl := range summary.OrphanedACLs {
					fmt.Printf("  - %s\n", acl)
				}
			}

			if len(summary.OrphanedVRFs) > 0 {
				fmt.Printf("\nOrphaned VRFs (%d):\n", len(summary.OrphanedVRFs))
				for _, vrf := range summary.OrphanedVRFs {
					fmt.Printf("  - %s\n", vrf)
				}
			}

			if len(summary.OrphanedVNIMappings) > 0 {
				fmt.Printf("\nOrphaned VNI Mappings (%d):\n", len(summary.OrphanedVNIMappings))
				for _, vni := range summary.OrphanedVNIMappings {
					fmt.Printf("  - %s\n", vni)
				}
			}
			fmt.Println()

			return cs, nil
		})
	},
}

func init() {
	deviceCleanupCmd.Flags().StringVar(&cleanupType, "type", "", "Cleanup specific type only (acls, vrfs, vnis)")

	deviceCmd.AddCommand(deviceCleanupCmd)
}
