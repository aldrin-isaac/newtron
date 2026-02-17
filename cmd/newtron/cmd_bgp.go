package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
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

		if app.jsonOutput {
			type bgpNeighborJSON struct {
				Address   string `json:"address"`
				RemoteAS  string `json:"remote_as"`
				Type      string `json:"type"`
				LocalAddr string `json:"local_addr,omitempty"`
				Admin     string `json:"admin_status"`
			}
			type bgpStatusJSON struct {
				LocalAS    int                `json:"local_as"`
				RouterID   string             `json:"router_id"`
				LoopbackIP string             `json:"loopback_ip"`
				Neighbors  []bgpNeighborJSON  `json:"neighbors,omitempty"`
			}
			status := bgpStatusJSON{
				LocalAS:    resolved.UnderlayASN,
				RouterID:   resolved.RouterID,
				LoopbackIP: resolved.LoopbackIP,
			}
			if configDB != nil {
				for addr, neighbor := range configDB.BGPNeighbor {
					nType := bgpNeighborType(neighbor.LocalAddr, resolved.LoopbackIP)
					localAddr := neighbor.LocalAddr
					adminStatus := neighbor.AdminStatus
					if adminStatus == "" {
						adminStatus = "up"
					}
					status.Neighbors = append(status.Neighbors, bgpNeighborJSON{
						Address:   addr,
						RemoteAS:  neighbor.ASN,
						Type:      nType,
						LocalAddr: localAddr,
						Admin:     adminStatus,
					})
				}
			}
			_ = underlying // operational state not yet serialized
			return json.NewEncoder(os.Stdout).Encode(status)
		}

		// --- Local BGP Identity ---
		fmt.Printf("BGP Status for %s\n\n", bold(app.deviceName))
		fmt.Printf("Local AS: %d\n", resolved.UnderlayASN)
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
			t := cli.NewTable("NEIGHBOR", "TYPE", "REMOTE AS", "LOCAL ADDR", "DESCRIPTION", "ADMIN").WithPrefix("  ")

			for addr, neighbor := range configDB.BGPNeighbor {
				neighborType := bgpNeighborType(neighbor.LocalAddr, resolved.LoopbackIP)
				localAddr := neighbor.LocalAddr
				if localAddr == "" {
					localAddr = resolved.LoopbackIP
				}

				adminStatus := neighbor.AdminStatus
				if adminStatus == "" {
					adminStatus = "up"
				}
				adminStatus = formatAdminStatus(adminStatus)

				description := dash(neighbor.Name)

				t.Row(addr, neighborType, neighbor.ASN, localAddr, description, adminStatus)
			}
			t.Flush()
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
				t := cli.NewTable("NEIGHBOR", "REMOTE AS", "STATE", "PFX RCVD", "PFX SENT", "UPTIME").WithPrefix("  ")

				for _, neighbor := range bgp.Neighbors {
					state := neighbor.State
					if state == "Established" {
						state = green(state)
					} else if state != "" {
						state = red(state)
					}

					uptime := dash(neighbor.Uptime)

					t.Row(
						neighbor.Address,
						fmt.Sprintf("%d", neighbor.RemoteAS),
						state,
						fmt.Sprintf("%d", neighbor.PfxRcvd),
						fmt.Sprintf("%d", neighbor.PfxSent),
						uptime,
					)
				}
				t.Flush()
			}
		}

		// --- Expected EVPN Neighbors ---
		if len(resolved.BGPNeighbors) > 0 {
			fmt.Printf("\nExpected EVPN neighbors (from EVPN peers):\n")
			for _, neighbor := range resolved.BGPNeighbors {
				fmt.Printf("  %s\n", neighbor)
			}
		}

		return nil
	},
}

// bgpNeighborType classifies a neighbor as "direct" (interface-level peering)
// or "indirect" (loopback-sourced, typically iBGP overlay).
func bgpNeighborType(localAddr, loopbackIP string) string {
	if localAddr != "" && localAddr != loopbackIP {
		return "direct"
	}
	return "indirect"
}

func init() {
	bgpCmd.AddCommand(bgpStatusCmd)
}
