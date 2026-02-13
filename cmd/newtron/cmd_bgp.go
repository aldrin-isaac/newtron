package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var bgpCmd = &cobra.Command{
	Use:   "bgp",
	Short: "BGP visibility (read-only)",
	Long: `View BGP configuration and operational state.

BGP is visibility-only in this noun group. Peer management lives in
'vrf add-neighbor' (direct, interface-level) and 'evpn setup' (overlay).

Requires -d (device) flag.

Examples:
  newtron leaf1 bgp status`,
}

// ============================================================================
// bgp status — unified view: summary + neighbors + operational state
// ============================================================================

var bgpStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show BGP config and operational state",
	Long: `Show a unified BGP status view combining:
  - Local BGP identity (AS, router ID, loopback IP)
  - Configured neighbors (from CONFIG_DB) with type classification
  - Operational neighbor state (from STATE_DB) with session info

Requires -d (device) flag.

Examples:
  newtron leaf1 bgp status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		resolved := dev.Resolved()
		configDB := dev.ConfigDB()
		underlying := dev.Underlying()

		// --- Local BGP Identity ---
		fmt.Printf("BGP Status for %s\n\n", bold(deviceName))
		fmt.Printf("Local AS: %d\n", resolved.ASNumber)
		fmt.Printf("Router ID: %s\n", resolved.RouterID)
		fmt.Printf("Loopback IP: %s\n", resolved.LoopbackIP)

		// --- Neighbor Summary ---
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
		fmt.Printf("\nNeighbors: %d total (%d direct, %d indirect)\n", neighborCount, directCount, indirectCount)

		// --- Configured Neighbors Table ---
		if configDB != nil && len(configDB.BGPNeighbor) > 0 {
			fmt.Println("\nConfigured Neighbors:")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  NEIGHBOR\tTYPE\tREMOTE AS\tLOCAL ADDR\tDESCRIPTION\tADMIN")
			fmt.Fprintln(w, "  --------\t----\t---------\t----------\t-----------\t-----")

			for addr, neighbor := range configDB.BGPNeighbor {
				// Determine neighbor type based on local_addr
				neighborType := "indirect"
				localAddr := neighbor.LocalAddr
				if localAddr == "" {
					localAddr = resolved.LoopbackIP
				} else if localAddr != resolved.LoopbackIP {
					neighborType = "direct"
				}

				adminStatus := neighbor.AdminStatus
				if adminStatus == "" {
					adminStatus = "up"
				}
				if adminStatus == "up" {
					adminStatus = green("up")
				} else {
					adminStatus = red(adminStatus)
				}

				description := neighbor.Name
				if description == "" {
					description = "-"
				}

				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
					addr, neighborType, neighbor.ASN, localAddr, description, adminStatus)
			}
			w.Flush()
		}

		// --- Operational State ---
		if underlying != nil && underlying.State != nil && underlying.State.BGP != nil {
			bgp := underlying.State.BGP

			establishedCount := 0
			for _, neighbor := range bgp.Neighbors {
				if neighbor.State == "Established" {
					establishedCount++
				}
			}

			fmt.Printf("\nOperational State (AS %d, Router ID %s):\n", bgp.LocalAS, bgp.RouterID)
			fmt.Printf("  Sessions: %d total, %d established\n", len(bgp.Neighbors), establishedCount)

			if len(bgp.Neighbors) > 0 {
				fmt.Println()
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "  NEIGHBOR\tREMOTE AS\tSTATE\tPFX RCVD\tPFX SENT\tUPTIME")
				fmt.Fprintln(w, "  --------\t---------\t-----\t--------\t--------\t------")

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

					fmt.Fprintf(w, "  %s\t%d\t%s\t%d\t%d\t%s\n",
						neighbor.Address,
						neighbor.RemoteAS,
						state,
						neighbor.PfxRcvd,
						neighbor.PfxSent,
						uptime,
					)
				}
				w.Flush()
			}
		}

		// --- Expected EVPN Neighbors ---
		if len(resolved.BGPNeighbors) > 0 {
			fmt.Printf("\nExpected EVPN neighbors (from site config):\n")
			for _, neighbor := range resolved.BGPNeighbors {
				fmt.Printf("  %s\n", neighbor)
			}
		}

		return nil
	},
}

func init() {
	bgpCmd.AddCommand(bgpStatusCmd)
}
