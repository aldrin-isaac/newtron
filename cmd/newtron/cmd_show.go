package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// showCmd displays device details.
// Top-level because "newtron -D leaf1 show" is the most natural entry point.
var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show device details",
	Long: `Show details of the selected device.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny show`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		info, err := app.client.DeviceInfo(app.deviceName)
		if err != nil {
			return err
		}

		return showDevice(info)
	},
}

func showDevice(info *newtron.DeviceInfo) error {
	if app.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(info)
	}

	fmt.Printf("Device: %s\n", bold(info.Name))
	fmt.Printf("Management IP: %s\n", info.MgmtIP)
	fmt.Printf("Loopback IP: %s\n", info.LoopbackIP)
	fmt.Printf("Platform: %s\n", info.Platform)
	fmt.Printf("Zone: %s\n", info.Zone)

	fmt.Println("\nDerived Configuration:")
	fmt.Printf("  BGP Local AS: %d\n", info.BGPAS)
	fmt.Printf("  BGP Router ID: %s\n", info.RouterID)
	fmt.Printf("  VTEP Source: %s via Loopback0\n", info.VTEPSourceIP)

	if len(info.BGPNeighbors) > 0 {
		fmt.Printf("  BGP EVPN Neighbors: %v\n", info.BGPNeighbors)
	}

	fmt.Println("\nState:")
	fmt.Printf("  Interfaces: %d\n", info.InterfaceCount)
	fmt.Printf("  PortChannels: %d\n", info.PortChannelCount)
	fmt.Printf("  VLANs: %d\n", info.VLANCount)
	fmt.Printf("  VRFs: %d\n", info.VRFCount)

	return nil
}
