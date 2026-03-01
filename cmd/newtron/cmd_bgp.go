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
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		status, err := n.BGPStatus()
		if err != nil {
			return fmt.Errorf("getting BGP status: %w", err)
		}

		if app.jsonOutput {
			type bgpNeighborJSON struct {
				Address   string `json:"address"`
				RemoteAS  string `json:"remote_as"`
				Type      string `json:"type"`
				LocalAddr string `json:"local_addr,omitempty"`
				Admin     string `json:"admin_status"`
			}
			type bgpStatusJSON struct {
				LocalAS    int               `json:"local_as"`
				RouterID   string            `json:"router_id"`
				LoopbackIP string            `json:"loopback_ip"`
				Neighbors  []bgpNeighborJSON `json:"neighbors,omitempty"`
			}
			out := bgpStatusJSON{
				LocalAS:    status.LocalAS,
				RouterID:   status.RouterID,
				LoopbackIP: status.LoopbackIP,
			}
			for _, nb := range status.Neighbors {
				out.Neighbors = append(out.Neighbors, bgpNeighborJSON{
					Address:   nb.Address,
					RemoteAS:  nb.RemoteAS,
					Type:      nb.Type,
					LocalAddr: nb.LocalAddr,
					Admin:     nb.Admin,
				})
			}
			// operational state not yet serialized in JSON output
			return json.NewEncoder(os.Stdout).Encode(out)
		}

		// --- Local BGP Identity ---
		fmt.Printf("BGP Status for %s\n\n", bold(app.deviceName))
		fmt.Printf("Local AS: %d\n", status.LocalAS)
		fmt.Printf("Router ID: %s\n", status.RouterID)
		fmt.Printf("Loopback IP: %s\n", status.LoopbackIP)

		// --- Neighbor Summary ---
		neighborCount := len(status.Neighbors)
		directCount := 0
		indirectCount := 0
		for _, nb := range status.Neighbors {
			if nb.Type == "direct" {
				directCount++
			} else {
				indirectCount++
			}
		}
		fmt.Printf("\nNeighbors: %d total (%d direct, %d indirect)\n", neighborCount, directCount, indirectCount)

		// --- Configured Neighbors Table ---
		if len(status.Neighbors) > 0 {
			fmt.Println("\nConfigured Neighbors:")
			t := cli.NewTable("NEIGHBOR", "TYPE", "REMOTE AS", "LOCAL ADDR", "DESCRIPTION", "ADMIN").WithPrefix("  ")

			for _, nb := range status.Neighbors {
				localAddr := nb.LocalAddr
				if localAddr == "" {
					localAddr = status.LoopbackIP
				}
				adminStatus := formatAdminStatus(nb.Admin)
				description := dash(nb.Name)
				t.Row(nb.Address, nb.Type, nb.RemoteAS, localAddr, description, adminStatus)
			}
			t.Flush()
		}

		// --- Operational State ---
		hasOpState := false
		for _, nb := range status.Neighbors {
			if nb.State != "" {
				hasOpState = true
				break
			}
		}

		if hasOpState {
			establishedCount := 0
			for _, nb := range status.Neighbors {
				if nb.State == "Established" {
					establishedCount++
				}
			}

			fmt.Printf("\nOperational State:\n")
			fmt.Printf("  Sessions: %d total, %d established\n", neighborCount, establishedCount)
			fmt.Println()

			t := cli.NewTable("NEIGHBOR", "REMOTE AS", "STATE", "PFX RCVD", "PFX SENT", "UPTIME").WithPrefix("  ")
			for _, nb := range status.Neighbors {
				if nb.State == "" {
					continue
				}
				state := nb.State
				if state == "Established" {
					state = green(state)
				} else {
					state = red(state)
				}
				t.Row(
					nb.Address,
					nb.RemoteAS,
					state,
					nb.PfxRcvd,
					nb.PfxSent,
					dash(nb.Uptime),
				)
			}
			t.Flush()
		}

		// --- Expected EVPN Neighbors ---
		if len(status.EVPNPeers) > 0 {
			fmt.Printf("\nExpected EVPN neighbors (from EVPN peers):\n")
			for _, neighbor := range status.EVPNPeers {
				fmt.Printf("  %s\n", neighbor)
			}
		}

		return nil
	},
}

func init() {
	bgpCmd.AddCommand(bgpStatusCmd)
}
