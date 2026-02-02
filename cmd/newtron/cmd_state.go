package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Show device operational state",
	Long: `Show device operational state from STATE_DB.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny state show
  newtron -d leaf1-ny state bgp
  newtron -d leaf1-ny state evpn
  newtron -d leaf1-ny state lag`,
}

var stateShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show overall device state summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		underlying := dev.Underlying()
		if underlying == nil {
			return fmt.Errorf("device connection not available")
		}

		fmt.Printf("Device State: %s\n", bold(deviceName))
		fmt.Println()

		if !underlying.HasStateDB() {
			fmt.Println("State DB not available (device may not support it)")
			return nil
		}

		// Interface summary
		if underlying.State != nil {
			upCount := 0
			downCount := 0
			for _, intf := range underlying.State.Interfaces {
				if intf.OperStatus == "up" {
					upCount++
				} else {
					downCount++
				}
			}
			fmt.Printf("Interfaces: %d up, %d down\n", upCount, downCount)
		}

		// LAG summary
		if underlying.State != nil && len(underlying.State.PortChannels) > 0 {
			activeCount := 0
			for _, pc := range underlying.State.PortChannels {
				if pc.OperStatus == "up" {
					activeCount++
				}
			}
			fmt.Printf("Port Channels: %d total, %d active\n", len(underlying.State.PortChannels), activeCount)
		}

		// BGP summary
		if underlying.State != nil && underlying.State.BGP != nil {
			bgp := underlying.State.BGP
			establishedCount := 0
			for _, neighbor := range bgp.Neighbors {
				if neighbor.State == "Established" {
					establishedCount++
				}
			}
			fmt.Printf("BGP: AS %d, Router ID %s\n", bgp.LocalAS, bgp.RouterID)
			fmt.Printf("BGP Neighbors: %d total, %d established\n", len(bgp.Neighbors), establishedCount)
		}

		// EVPN summary
		if underlying.State != nil && underlying.State.EVPN != nil {
			evpn := underlying.State.EVPN
			fmt.Printf("EVPN: VTEP %s, VNIs: %d, Remote VTEPs: %d\n",
				evpn.VTEPState, evpn.VNICount, len(evpn.RemoteVTEPs))
		}

		// VRF summary
		if underlying.State != nil && len(underlying.State.VRFs) > 0 {
			fmt.Printf("VRFs: %d configured\n", len(underlying.State.VRFs))
		}

		return nil
	},
}

var stateBGPCmd = &cobra.Command{
	Use:   "bgp",
	Short: "Show BGP neighbor state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		underlying := dev.Underlying()
		if underlying == nil || underlying.State == nil || underlying.State.BGP == nil {
			fmt.Println("BGP state not available")
			return nil
		}

		bgp := underlying.State.BGP
		fmt.Printf("BGP State for %s\n", bold(deviceName))
		fmt.Printf("Local AS: %d\n", bgp.LocalAS)
		fmt.Printf("Router ID: %s\n\n", bgp.RouterID)

		if len(bgp.Neighbors) == 0 {
			fmt.Println("No BGP neighbors")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NEIGHBOR\tREMOTE AS\tSTATE\tPFX RCVD\tPFX SENT\tUPTIME")
		fmt.Fprintln(w, "--------\t---------\t-----\t--------\t--------\t------")

		for _, neighbor := range bgp.Neighbors {
			state := neighbor.State
			if state == "Established" {
				state = green(state)
			} else if state != "" {
				state = red(state)
			}

			uptime := neighbor.Uptime
			if uptime == "" {
				uptime = "-"
			}

			fmt.Fprintf(w, "%s\t%d\t%s\t%d\t%d\t%s\n",
				neighbor.Address,
				neighbor.RemoteAS,
				state,
				neighbor.PfxRcvd,
				neighbor.PfxSent,
				uptime,
			)
		}
		w.Flush()

		return nil
	},
}

var stateEVPNCmd = &cobra.Command{
	Use:   "evpn",
	Short: "Show EVPN state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		underlying := dev.Underlying()
		if underlying == nil || underlying.State == nil || underlying.State.EVPN == nil {
			fmt.Println("EVPN state not available")
			return nil
		}

		evpn := underlying.State.EVPN
		fmt.Printf("EVPN State for %s\n", bold(deviceName))

		vtepState := evpn.VTEPState
		if vtepState == "up" || vtepState == "oper_up" {
			vtepState = green(vtepState)
		} else if vtepState != "" {
			vtepState = red(vtepState)
		} else {
			vtepState = "-"
		}

		fmt.Printf("VTEP Status: %s\n", vtepState)
		fmt.Printf("VNI Count: %d\n", evpn.VNICount)
		fmt.Printf("Type-2 Routes: %d\n", evpn.Type2Routes)
		fmt.Printf("Type-5 Routes: %d\n", evpn.Type5Routes)

		if len(evpn.RemoteVTEPs) > 0 {
			fmt.Printf("\nRemote VTEPs (%d):\n", len(evpn.RemoteVTEPs))
			for _, vtep := range evpn.RemoteVTEPs {
				fmt.Printf("  %s\n", vtep)
			}
		} else {
			fmt.Println("\nNo remote VTEPs discovered")
		}

		return nil
	},
}

var stateLAGCmd = &cobra.Command{
	Use:   "lag",
	Short: "Show LAG member state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		underlying := dev.Underlying()
		if underlying == nil || underlying.State == nil {
			fmt.Println("State not available")
			return nil
		}

		if len(underlying.State.PortChannels) == 0 {
			fmt.Println("No PortChannels configured")
			return nil
		}

		fmt.Printf("LAG State for %s\n\n", bold(deviceName))

		for name, pc := range underlying.State.PortChannels {
			operStatus := pc.OperStatus
			if operStatus == "up" {
				operStatus = green(operStatus)
			} else if operStatus != "" {
				operStatus = red(operStatus)
			}

			fmt.Printf("%s (admin: %s, oper: %s)\n", bold(name), pc.AdminStatus, operStatus)
			fmt.Printf("  Members: %d total, %d active\n", len(pc.Members), len(pc.ActiveMembers))

			if len(pc.Members) > 0 {
				fmt.Println("  Member Status:")
				for _, member := range pc.Members {
					isActive := false
					for _, active := range pc.ActiveMembers {
						if active == member {
							isActive = true
							break
						}
					}
					if isActive {
						fmt.Printf("    %s: %s\n", member, green("active"))
					} else {
						fmt.Printf("    %s: %s\n", member, red("standby"))
					}
				}
			}
			fmt.Println()
		}

		return nil
	},
}

var stateVRFCmd = &cobra.Command{
	Use:   "vrf",
	Short: "Show VRF state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		underlying := dev.Underlying()
		if underlying == nil || underlying.State == nil {
			fmt.Println("State not available")
			return nil
		}

		if len(underlying.State.VRFs) == 0 {
			fmt.Println("No VRFs configured")
			return nil
		}

		fmt.Printf("VRF State for %s\n\n", bold(deviceName))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "VRF\tSTATE\tL3VNI\tINTERFACES\tROUTES")
		fmt.Fprintln(w, "---\t-----\t-----\t----------\t------")

		for name, vrf := range underlying.State.VRFs {
			state := vrf.State
			if state == "active" || state == "up" {
				state = green(state)
			} else if state != "" {
				state = red(state)
			} else {
				state = "-"
			}

			l3vni := "-"
			if vrf.L3VNI > 0 {
				l3vni = fmt.Sprintf("%d", vrf.L3VNI)
			}

			intfCount := fmt.Sprintf("%d", len(vrf.Interfaces))
			routeCount := fmt.Sprintf("%d", vrf.RouteCount)

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				name, state, l3vni, intfCount, routeCount)
		}
		w.Flush()

		return nil
	},
}

func init() {
	stateCmd.AddCommand(stateShowCmd)
	stateCmd.AddCommand(stateBGPCmd)
	stateCmd.AddCommand(stateEVPNCmd)
	stateCmd.AddCommand(stateLAGCmd)
	stateCmd.AddCommand(stateVRFCmd)
}
