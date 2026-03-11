package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize device for newtron management",
	Long: `Prepare a SONiC device for newtron management by enabling frrcfgd.

SONiC ships with bgpcfgd by default, which silently ignores dynamic
CONFIG_DB entries (BGP_NEIGHBOR, BGP_GLOBALS, etc.). newtron requires
frrcfgd (unified config mode) so all CONFIG_DB writes are processed by FRR.

This command:
  1. Sets docker_routing_config_mode=unified in DEVICE_METADATA
  2. Restarts the bgp container so frrcfgd takes over
  3. Saves config to persist across reboots

WARNING: Initialization restarts the bgp container. On a device with active
BGP sessions, this causes all sessions to drop and reconverge. Use --force
to proceed on devices with existing BGP configuration.

Safe to run on devices that were already initialized — detects and skips.
No topology required — works with any device that has a profile.

Examples:
  newtron switch1 init
  newtron leaf1-ny init
  newtron leaf1-ny init --force    # proceed despite active BGP sessions`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		fmt.Printf("Initializing %s for newtron management...\n", bold(app.deviceName))

		status, err := app.client.InitDevice(app.deviceName, initForce)
		if err != nil {
			return err
		}

		switch status {
		case "already_initialized":
			fmt.Printf("%s is already initialized (frrcfgd enabled).\n", app.deviceName)
		default:
			fmt.Printf("%s\n", green("Initialized."))
			fmt.Println("  DEVICE_METADATA: docker_routing_config_mode=unified")
			fmt.Println("  bgp container restarted, frrcfgd running")
			fmt.Println("  Config saved to persist across reboots")
		}

		return nil
	},
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, "Proceed even if device has active BGP sessions")
}
