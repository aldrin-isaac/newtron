package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/network"
)

// showCmd displays device details.
// Top-level because "newtron -d leaf1 show" is the most natural entry point.
var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show device details",
	Long: `Show details of the selected device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny show`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		if deviceName == "" {
			return fmt.Errorf("device required: use -d <device> flag")
		}

		dev, err := net.ConnectDevice(ctx, deviceName)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		return showDevice(dev)
	},
}

func showDevice(dev *network.Device) error {
	fmt.Printf("Device: %s\n", bold(dev.Name()))
	fmt.Printf("Management IP: %s\n", dev.Profile().MgmtIP)
	fmt.Printf("Loopback IP: %s\n", dev.Profile().LoopbackIP)
	fmt.Printf("Platform: %s\n", dev.Profile().Platform)
	fmt.Printf("Site: %s\n", dev.Profile().Site)

	fmt.Println("\nDerived Configuration:")
	fmt.Printf("  BGP Local AS: %d\n", dev.ASNumber())
	fmt.Printf("  BGP Router ID: %s\n", dev.RouterID())
	fmt.Printf("  VTEP Source: %s via Loopback0\n", dev.VTEPSourceIP())

	if neighbors := dev.BGPNeighbors(); len(neighbors) > 0 {
		fmt.Printf("  BGP EVPN Neighbors: %v\n", neighbors)
	}

	fmt.Println("\nState:")
	fmt.Printf("  Interfaces: %d\n", len(dev.ListInterfaces()))
	fmt.Printf("  PortChannels: %d\n", len(dev.ListPortChannels()))
	fmt.Printf("  VLANs: %d\n", len(dev.ListVLANs()))
	fmt.Printf("  VRFs: %d\n", len(dev.ListVRFs()))

	return nil
}
