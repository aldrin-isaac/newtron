package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
			// operational state not yet serialized in JSON output
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
		stateClient := dev.StateDBClient()
		if configDB != nil && stateClient != nil && len(configDB.BGPNeighbor) > 0 {
			type neighborState struct {
				address  string
				remoteAS string
				state    string
				pfxRcvd  string
				pfxSent  string
				uptime   string
			}

			var neighbors []neighborState
			establishedCount := 0
			for key := range configDB.BGPNeighbor {
				// Key format: "vrf|neighborIP"
				parts := strings.SplitN(key, "|", 2)
				if len(parts) != 2 {
					continue
				}
				vrf, neighborIP := parts[0], parts[1]
				entry, err := stateClient.GetBGPNeighborState(vrf, neighborIP)
				if err != nil {
					continue
				}
				ns := neighborState{
					address:  neighborIP,
					remoteAS: entry.RemoteAS,
					state:    entry.State,
					pfxRcvd:  entry.PfxRcvd,
					pfxSent:  entry.PfxSent,
					uptime:   entry.Uptime,
				}
				if entry.State == "Established" {
					establishedCount++
				}
				neighbors = append(neighbors, ns)
			}

			fmt.Printf("\nOperational State:\n")
			fmt.Printf("  Sessions: %d total, %d established\n", len(neighbors), establishedCount)

			if len(neighbors) > 0 {
				fmt.Println()
				t := cli.NewTable("NEIGHBOR", "REMOTE AS", "STATE", "PFX RCVD", "PFX SENT", "UPTIME").WithPrefix("  ")

				for _, ns := range neighbors {
					state := ns.state
					if state == "Established" {
						state = green(state)
					} else if state != "" {
						state = red(state)
					}

					uptime := dash(ns.uptime)

					t.Row(
						ns.address,
						ns.remoteAS,
						state,
						ns.pfxRcvd,
						ns.pfxSent,
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
