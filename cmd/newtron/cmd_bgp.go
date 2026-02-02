package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/network"
)

var bgpCmd = &cobra.Command{
	Use:   "bgp",
	Short: "Manage BGP configuration",
	Long: `Manage BGP configuration and neighbors.

BGP neighbors are categorized as:
  - Direct (interface-level): eBGP peers using link IP as update-source
  - Indirect (device-level): iBGP peers using loopback IP as update-source

Requires -d (device) flag. Use -i (interface) for direct neighbor operations.

Examples:
  newtron -d leaf1-ny bgp neighbors
  newtron -d leaf1-ny bgp summary
  newtron -d leaf1-ny -i Ethernet0 bgp add-direct 65100 --description "customer-a"
  newtron -d leaf1-ny bgp add-loopback 10.0.0.2 --evpn --description "spine1"
  newtron -d leaf1-ny bgp setup-evpn`,
}

var bgpNeighborsCmd = &cobra.Command{
	Use:   "neighbors",
	Short: "List BGP neighbors",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		resolved := dev.Resolved()
		fmt.Printf("BGP Neighbors for %s\n", bold(deviceName))
		fmt.Printf("Local AS: %d\n", resolved.ASNumber)
		fmt.Printf("Router ID: %s\n", resolved.RouterID)
		fmt.Printf("Loopback IP: %s\n\n", resolved.LoopbackIP)

		configDB := dev.ConfigDB()
		if configDB == nil || len(configDB.BGPNeighbor) == 0 {
			fmt.Println("No BGP neighbors configured")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NEIGHBOR\tTYPE\tREMOTE AS\tLOCAL ADDR\tDESCRIPTION\tSTATUS")
		fmt.Fprintln(w, "--------\t----\t---------\t----------\t-----------\t------")

		for addr, neighbor := range configDB.BGPNeighbor {
			// Determine neighbor type based on local_addr
			neighborType := "indirect"
			localAddr := neighbor.LocalAddr
			if localAddr == "" {
				localAddr = resolved.LoopbackIP
			} else if localAddr != resolved.LoopbackIP {
				neighborType = "direct"
			}

			status := neighbor.AdminStatus
			if status == "" {
				status = "up"
			}
			if status == "up" {
				status = green("up")
			} else {
				status = red(status)
			}

			description := neighbor.Name
			if description == "" {
				description = "-"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				addr, neighborType, neighbor.ASN, localAddr, description, status)
		}
		w.Flush()

		return nil
	},
}

var bgpSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Show BGP summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		resolved := dev.Resolved()
		configDB := dev.ConfigDB()

		fmt.Printf("BGP Summary for %s\n", bold(deviceName))
		fmt.Printf("Local AS: %d\n", resolved.ASNumber)
		fmt.Printf("Router ID: %s\n", resolved.RouterID)
		fmt.Printf("Loopback IP: %s\n", resolved.LoopbackIP)

		neighborCount := 0
		directCount := 0
		indirectCount := 0
		if configDB != nil {
			for _, neighbor := range configDB.BGPNeighbor {
				neighborCount++
				if neighbor.LocalAddr != "" && neighbor.LocalAddr != resolved.LoopbackIP {
					directCount++
				} else {
					indirectCount++
				}
			}
		}
		fmt.Printf("\nNeighbors configured: %d total\n", neighborCount)
		fmt.Printf("  Direct (interface IP): %d\n", directCount)
		fmt.Printf("  Indirect (loopback IP): %d\n", indirectCount)

		// Show expected EVPN neighbors from profile
		if len(resolved.BGPNeighbors) > 0 {
			fmt.Printf("\nExpected EVPN neighbors (from site config):\n")
			for _, neighbor := range resolved.BGPNeighbors {
				fmt.Printf("  %s\n", neighbor)
			}
		}

		return nil
	},
}

// Direct neighbor commands (interface-level)
var bgpAddDirectCmd = &cobra.Command{
	Use:   "add-direct <remote-asn>",
	Short: "Add a direct BGP neighbor on an interface",
	Long: `Add a direct BGP neighbor using the interface IP as update-source.

This is typically used for eBGP peering over point-to-point links.
The neighbor IP is auto-derived for /30 and /31 subnets.

Requires both -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 bgp add-direct 65100
  newtron -d leaf1-ny -i Ethernet0 bgp add-direct 65100 --neighbor 10.1.1.2 --description "customer-a"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		asnStr := args[0]

		asn, err := strconv.Atoi(asnStr)
		if err != nil {
			return fmt.Errorf("invalid ASN: %s", asnStr)
		}

		neighborIP, _ := cmd.Flags().GetString("neighbor")
		description, _ := cmd.Flags().GetString("description")

		ctx := context.Background()
		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		cfg := network.DirectBGPNeighborConfig{
			NeighborIP:  neighborIP,
			RemoteAS:    asn,
			Description: description,
		}

		cs, err := intf.AddBGPNeighbor(ctx, cfg)
		if err != nil {
			return fmt.Errorf("adding neighbor: %w", err)
		}

		// Preview changes
		fmt.Println(cs.Preview())

		if !executeMode {
			printDryRunNotice()
			return nil
		}

		if err := cs.Apply(dev); err != nil {
			return fmt.Errorf("applying changes: %w", err)
		}

		fmt.Printf("\nDirect BGP neighbor added on %s successfully.\n", interfaceName)
		return nil
	},
}

var bgpRemoveDirectCmd = &cobra.Command{
	Use:   "remove-direct [neighbor-ip]",
	Short: "Remove a direct BGP neighbor from an interface",
	Long: `Remove a direct BGP neighbor from the interface.

If neighbor-ip is not specified, removes the auto-derived neighbor.

Requires both -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 bgp remove-direct
  newtron -d leaf1-ny -i Ethernet0 bgp remove-direct 10.1.1.2`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		neighborIP := ""
		if len(args) > 0 {
			neighborIP = args[0]
		}

		ctx := context.Background()
		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		cs, err := intf.RemoveBGPNeighbor(ctx, neighborIP)
		if err != nil {
			return fmt.Errorf("removing neighbor: %w", err)
		}

		// Preview changes
		fmt.Println(cs.Preview())

		if !executeMode {
			printDryRunNotice()
			return nil
		}

		if err := cs.Apply(dev); err != nil {
			return fmt.Errorf("applying changes: %w", err)
		}

		fmt.Printf("\nDirect BGP neighbor removed from %s successfully.\n", interfaceName)
		return nil
	},
}

// Indirect neighbor commands (device-level via loopback)
var bgpAddLoopbackCmd = &cobra.Command{
	Use:   "add-loopback <neighbor-ip>",
	Short: "Add an indirect BGP neighbor using loopback as update-source",
	Long: `Add an indirect BGP neighbor using the device's loopback IP as update-source.

This is typically used for:
  - iBGP peering (same AS, loopback-to-loopback)
  - EVPN route reflector peering
  - Multi-hop eBGP

The remote AS defaults to the local AS (iBGP) unless --asn is specified.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny bgp add-loopback 10.0.0.2 --evpn --description "spine1"
  newtron -d leaf1-ny bgp add-loopback 10.0.0.2 --asn 65200 --description "remote-peer"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		neighborIP := args[0]

		description, _ := cmd.Flags().GetString("description")
		evpn, _ := cmd.Flags().GetBool("evpn")
		asnOverride, _ := cmd.Flags().GetInt("asn")

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		// Default to iBGP (same AS) if not specified
		asn := dev.Resolved().ASNumber
		if asnOverride > 0 {
			asn = asnOverride
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		cs, err := dev.AddLoopbackBGPNeighbor(ctx, neighborIP, asn, description, evpn)
		if err != nil {
			return fmt.Errorf("adding neighbor: %w", err)
		}

		// Preview changes
		fmt.Println(cs.Preview())

		if !executeMode {
			printDryRunNotice()
			return nil
		}

		if err := cs.Apply(dev); err != nil {
			return fmt.Errorf("applying changes: %w", err)
		}

		fmt.Printf("\nLoopback BGP neighbor %s added successfully.\n", neighborIP)
		return nil
	},
}

var bgpRemoveNeighborCmd = &cobra.Command{
	Use:   "remove-neighbor <neighbor-ip>",
	Short: "Remove a BGP neighbor (any type)",
	Long: `Remove a BGP neighbor by IP address.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny bgp remove-neighbor 10.0.0.2`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		neighborIP := args[0]

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		cs, err := dev.RemoveBGPNeighbor(ctx, neighborIP)
		if err != nil {
			return fmt.Errorf("removing neighbor: %w", err)
		}

		// Preview changes
		fmt.Println(cs.Preview())

		if !executeMode {
			printDryRunNotice()
			return nil
		}

		if err := cs.Apply(dev); err != nil {
			return fmt.Errorf("applying changes: %w", err)
		}

		fmt.Printf("\nBGP neighbor %s removed successfully.\n", neighborIP)
		return nil
	},
}

var bgpSetupEVPNCmd = &cobra.Command{
	Use:   "setup-evpn",
	Short: "Configure BGP EVPN with route reflectors from site config",
	Long: `Sets up BGP EVPN peering with route reflectors defined in the site configuration.

This command creates indirect (loopback-based) iBGP sessions for EVPN:
1. Configures BGP with the device's AS number and router ID
2. Creates EVPN neighbors with the site's route reflectors
3. Uses loopback IP as update-source for all neighbors
4. Activates L2VPN EVPN address family

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny bgp setup-evpn`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		cs, err := dev.SetupBGPEVPN(ctx)
		if err != nil {
			return fmt.Errorf("setting up EVPN: %w", err)
		}

		// Preview changes
		fmt.Println(cs.Preview())

		if !executeMode {
			printDryRunNotice()
			return nil
		}

		if err := cs.Apply(dev); err != nil {
			return fmt.Errorf("applying changes: %w", err)
		}

		fmt.Println("\nBGP EVPN setup completed successfully.")
		return nil
	},
}

func init() {
	bgpCmd.AddCommand(bgpNeighborsCmd)
	bgpCmd.AddCommand(bgpSummaryCmd)
	bgpCmd.AddCommand(bgpAddDirectCmd)
	bgpCmd.AddCommand(bgpRemoveDirectCmd)
	bgpCmd.AddCommand(bgpAddLoopbackCmd)
	bgpCmd.AddCommand(bgpRemoveNeighborCmd)
	bgpCmd.AddCommand(bgpSetupEVPNCmd)

	// Direct neighbor flags
	bgpAddDirectCmd.Flags().String("neighbor", "", "Neighbor IP (auto-derived for /30, /31 if not specified)")
	bgpAddDirectCmd.Flags().StringP("description", "D", "", "Neighbor description")

	// Loopback neighbor flags
	bgpAddLoopbackCmd.Flags().StringP("description", "D", "", "Neighbor description")
	bgpAddLoopbackCmd.Flags().Bool("evpn", false, "Enable L2VPN EVPN address family")
	bgpAddLoopbackCmd.Flags().Int("asn", 0, "Remote AS number (defaults to local AS for iBGP)")
}
