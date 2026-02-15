package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
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

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		return showDevice(dev)
	},
}

func showDevice(dev *node.Node) error {
	if app.jsonOutput {
		data := map[string]any{
			"name":            dev.Name(),
			"mgmt_ip":         dev.Profile().MgmtIP,
			"loopback_ip":     dev.Profile().LoopbackIP,
			"platform":        dev.Profile().Platform,
			"zone":            dev.Profile().Zone,
			"bgp_as":          dev.ASNumber(),
			"router_id":       dev.RouterID(),
			"vtep_source_ip":  dev.VTEPSourceIP(),
			"bgp_neighbors":   dev.BGPNeighbors(),
			"interfaces":      len(dev.ListInterfaces()),
			"port_channels":   len(dev.ListPortChannels()),
			"vlans":           len(dev.ListVLANs()),
			"vrfs":            len(dev.ListVRFs()),
		}
		return json.NewEncoder(os.Stdout).Encode(data)
	}

	fmt.Printf("Device: %s\n", bold(dev.Name()))
	fmt.Printf("Management IP: %s\n", dev.Profile().MgmtIP)
	fmt.Printf("Loopback IP: %s\n", dev.Profile().LoopbackIP)
	fmt.Printf("Platform: %s\n", dev.Profile().Platform)
	fmt.Printf("Zone: %s\n", dev.Profile().Zone)

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
