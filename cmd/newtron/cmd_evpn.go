package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/cli"
)

var evpnCmd = &cobra.Command{
	Use:   "evpn",
	Short: "Manage EVPN overlay system",
	Long: `Manage the EVPN overlay system (VTEP + NVO + BGP EVPN).

EVPN is the overlay transport for VXLAN. The 'setup' command is an idempotent
composite that configures the full EVPN stack in one shot. The 'status' command
shows both config and operational state.

IP-VPN and MAC-VPN definitions are spec-level objects in network.json that
define L3 and L2 VPN parameters respectively. They do not require a device.

Examples:
  newtron leaf1 evpn setup -x
  newtron leaf1 evpn setup --source-ip 10.0.0.10 -x
  newtron leaf1 evpn status
  newtron evpn ipvpn list
  newtron evpn ipvpn create customer-vpn --l3vni 10001 -x
  newtron evpn macvpn list
  newtron evpn macvpn create servers-vlan100 --vni 1100 --vlan-id 100 --arp-suppress -x`,
}

// ============================================================================
// evpn setup — idempotent composite: VTEP + NVO + BGP EVPN
// ============================================================================

var evpnSetupSourceIP string

var evpnSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure EVPN overlay (VTEP + NVO + BGP EVPN)",
	Long: `Idempotent composite that configures the full EVPN stack:

1. Creates VXLAN Tunnel Endpoint (VTEP) with source IP
2. Creates EVPN NVO (Network Virtualization Overlay)
3. Configures BGP EVPN sessions with route reflectors from site config

If --source-ip is not specified, uses the device's loopback IP.
Skips any components that are already configured.

Requires -d (device) flag.

Examples:
  newtron leaf1 evpn setup -x
  newtron leaf1 evpn setup --source-ip 10.0.0.10 -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource("evpn")
			if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
				return err
			}
			if err := n.SetupEVPN(ctx, evpnSetupSourceIP); err != nil {
				return fmt.Errorf("setting up EVPN: %w", err)
			}
			return nil
		})
	},
}

// ============================================================================
// evpn status — unified config + operational state view
// ============================================================================

var evpnStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show EVPN config and operational state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		status, err := n.EVPNStatus()
		if err != nil {
			return fmt.Errorf("getting EVPN status: %w", err)
		}

		if app.jsonOutput {
			type evpnStatusJSON struct {
				VTEPs    map[string]string `json:"vteps,omitempty"`
				NVOs     map[string]string `json:"nvos,omitempty"`
				VNICount int               `json:"vni_count"`
			}
			out := evpnStatusJSON{
				VTEPs:    status.VTEPs,
				NVOs:     status.NVOs,
				VNICount: status.VNICount,
			}
			return json.NewEncoder(os.Stdout).Encode(out)
		}

		fmt.Printf("EVPN Status for %s\n\n", bold(app.deviceName))

		// --- VTEP Configuration ---
		fmt.Println("VTEP Configuration:")
		if len(status.VTEPs) == 0 {
			fmt.Println("  (not configured)")
		} else {
			for name, srcIP := range status.VTEPs {
				fmt.Printf("  %s: source_ip=%s\n", name, srcIP)
			}
		}

		// --- EVPN NVO ---
		fmt.Println("\nEVPN NVO:")
		if len(status.NVOs) == 0 {
			fmt.Println("  (not configured)")
		} else {
			for name, sourceVTEP := range status.NVOs {
				fmt.Printf("  %s: source_vtep=%s\n", name, sourceVTEP)
			}
		}

		// --- VNI Mappings ---
		fmt.Println("\nVNI Mappings:")
		if len(status.VNIMappings) == 0 {
			fmt.Println("  (none)")
		} else {
			t := cli.NewTable("VNI", "TYPE", "RESOURCE").WithPrefix("  ")
			for _, mapping := range status.VNIMappings {
				t.Row(mapping.VNI, mapping.Type, mapping.Resource)
			}
			t.Flush()
		}

		// --- VRFs with L3VNI ---
		fmt.Println("\nVRFs with L3VNI:")
		if len(status.L3VNIVRFs) == 0 {
			fmt.Println("  (none)")
		} else {
			for _, entry := range status.L3VNIVRFs {
				fmt.Printf("  %s: L3VNI=%d\n", entry.VRF, entry.L3VNI)
			}
		}

		// --- Operational State ---
		fmt.Println("\nOperational State:")
		vtepState := "-"
		if status.VTEPStatus != "" {
			vtepState = formatOperStatus(status.VTEPStatus)
		}
		fmt.Printf("  VTEP Status: %s\n", vtepState)
		fmt.Printf("  VNI Count: %d\n", status.VNICount)

		if len(status.RemoteVTEPs) > 0 {
			fmt.Printf("  Remote VTEPs (%d):\n", len(status.RemoteVTEPs))
			for _, vtep := range status.RemoteVTEPs {
				fmt.Printf("    %s\n", vtep)
			}
		} else {
			fmt.Println("  Remote VTEPs: (none)")
		}

		return nil
	},
}

// ============================================================================
// evpn ipvpn — spec authoring commands for IP-VPN definitions
// ============================================================================

var evpnIpvpnCmd = &cobra.Command{
	Use:   "ipvpn",
	Short: "Manage IP-VPN definitions (network.json)",
	Long: `Manage IP-VPN definitions in network.json.

IP-VPN defines L3 VPN parameters (L3VNI, route targets) used by services
and VRF bindings. These are spec-level objects that do not require a device.

Examples:
  newtron evpn ipvpn list
  newtron evpn ipvpn show customer-vpn
  newtron evpn ipvpn create customer-vpn --l3vni 10001 -x
  newtron evpn ipvpn delete customer-vpn -x`,
}

var evpnIpvpnListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all IP-VPN definitions",
	RunE: func(cmd *cobra.Command, args []string) error {
		ipvpns := app.net.ListIPVPNs()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(ipvpns)
		}

		if len(ipvpns) == 0 {
			fmt.Println("No IP-VPN definitions")
			return nil
		}

		t := cli.NewTable("NAME", "L3VNI", "VRF", "ROUTE TARGETS", "DESCRIPTION")

		for name, ipvpn := range ipvpns {
			rt := "-"
			if len(ipvpn.RouteTargets) > 0 {
				rt = strings.Join(ipvpn.RouteTargets, ",")
			}
			desc := dash(ipvpn.Description)
			t.Row(name, fmt.Sprintf("%d", ipvpn.L3VNI), dash(ipvpn.VRF), rt, desc)
		}
		t.Flush()

		return nil
	},
}

var evpnIpvpnShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show IP-VPN definition details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		ipvpn, err := app.net.ShowIPVPN(name)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(ipvpn)
		}

		fmt.Printf("IP-VPN: %s\n", bold(name))
		if ipvpn.Description != "" {
			fmt.Printf("Description: %s\n", ipvpn.Description)
		}
		fmt.Printf("L3VNI: %d\n", ipvpn.L3VNI)
		if ipvpn.VRF != "" {
			fmt.Printf("VRF: %s\n", ipvpn.VRF)
		}
		if len(ipvpn.RouteTargets) > 0 {
			fmt.Printf("Route Targets: %s\n", strings.Join(ipvpn.RouteTargets, ", "))
		}

		return nil
	},
}

var (
	ipvpnL3VNI        int
	ipvpnRouteTargets string
	ipvpnVRF          string
	ipvpnDescription  string
)

var evpnIpvpnCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an IP-VPN definition",
	Long: `Create an IP-VPN definition in network.json.

This is a spec authoring command that does not require a device connection.

Examples:
  newtron evpn ipvpn create customer-vpn --l3vni 10001 --vrf Vrf_cust -x
  newtron evpn ipvpn create customer-vpn --l3vni 10001 --vrf Vrf_cust --route-targets 65000:10001 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if ipvpnL3VNI <= 0 {
			return fmt.Errorf("--l3vni is required")
		}

		authCtx := auth.NewContext().WithResource(name)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		req := newtron.CreateIPVPNRequest{
			Name:        name,
			L3VNI:       ipvpnL3VNI,
			VRF:         ipvpnVRF,
			Description: ipvpnDescription,
		}
		if ipvpnRouteTargets != "" {
			req.RouteTargets = strings.Split(ipvpnRouteTargets, ",")
		}

		fmt.Printf("IP-VPN: %s\n", name)
		fmt.Printf("  L3VNI: %d\n", req.L3VNI)
		if req.VRF != "" {
			fmt.Printf("  VRF: %s\n", req.VRF)
		}
		if len(req.RouteTargets) > 0 {
			fmt.Printf("  Route Targets: %v\n", req.RouteTargets)
		}
		if req.Description != "" {
			fmt.Printf("  Description: %s\n", req.Description)
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.CreateIPVPN(req, newtron.ExecOpts{Execute: true}); err != nil {
			return fmt.Errorf("saving IP-VPN: %w", err)
		}
		fmt.Println("\n" + green("IP-VPN definition saved to network.json."))

		return nil
	},
}

var evpnIpvpnDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an IP-VPN definition",
	Long: `Delete an IP-VPN definition from network.json.

This is a spec authoring command that does not require a device connection.

Examples:
  newtron evpn ipvpn delete customer-vpn -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Verify it exists
		if _, err := app.net.ShowIPVPN(name); err != nil {
			return err
		}

		authCtx := auth.NewContext().WithResource(name)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		fmt.Printf("Deleting IP-VPN: %s\n", name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.DeleteIPVPN(name, newtron.ExecOpts{Execute: true}); err != nil {
			return fmt.Errorf("deleting IP-VPN: %w", err)
		}
		fmt.Println(green("IP-VPN definition deleted from network.json."))

		return nil
	},
}

// ============================================================================
// evpn macvpn — spec authoring commands for MAC-VPN definitions
// ============================================================================

var evpnMacvpnCmd = &cobra.Command{
	Use:   "macvpn",
	Short: "Manage MAC-VPN definitions (network.json)",
	Long: `Manage MAC-VPN definitions in network.json.

MAC-VPN defines L2 VPN parameters (VNI, VLAN, anycast gateway, ARP suppression)
used by services and VLAN bindings. These are spec-level objects that do not
require a device.

Examples:
  newtron evpn macvpn list
  newtron evpn macvpn show servers-vlan100
  newtron evpn macvpn create servers-vlan100 --vni 1100 --vlan-id 100 --arp-suppress -x
  newtron evpn macvpn delete servers-vlan100 -x`,
}

var evpnMacvpnListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all MAC-VPN definitions",
	RunE: func(cmd *cobra.Command, args []string) error {
		macvpns := app.net.ListMACVPNs()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(macvpns)
		}

		if len(macvpns) == 0 {
			fmt.Println("No MAC-VPN definitions")
			return nil
		}

		t := cli.NewTable("NAME", "VNI", "VLAN ID", "ANYCAST IP", "ARP SUPPRESS", "DESCRIPTION")

		for name, macvpn := range macvpns {
			arpSuppress := "no"
			if macvpn.ARPSuppression {
				arpSuppress = "yes"
			}
			desc := dash(macvpn.Description)
			t.Row(name, fmt.Sprintf("%d", macvpn.VNI), fmt.Sprintf("%d", macvpn.VlanID), dash(macvpn.AnycastIP), arpSuppress, desc)
		}
		t.Flush()

		return nil
	},
}

var evpnMacvpnShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show MAC-VPN definition details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		macvpn, err := app.net.ShowMACVPN(name)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(macvpn)
		}

		fmt.Printf("MAC-VPN: %s\n", bold(name))
		if macvpn.Description != "" {
			fmt.Printf("Description: %s\n", macvpn.Description)
		}
		fmt.Printf("VNI: %d\n", macvpn.VNI)
		fmt.Printf("VLAN ID: %d\n", macvpn.VlanID)
		if macvpn.AnycastIP != "" {
			fmt.Printf("Anycast IP: %s\n", macvpn.AnycastIP)
		}
		if macvpn.AnycastMAC != "" {
			fmt.Printf("Anycast MAC: %s\n", macvpn.AnycastMAC)
		}
		if len(macvpn.RouteTargets) > 0 {
			fmt.Printf("Route Targets: %s\n", strings.Join(macvpn.RouteTargets, ", "))
		}
		fmt.Printf("ARP Suppression: %v\n", macvpn.ARPSuppression)

		return nil
	},
}

var (
	macvpnVNI          int
	macvpnVlanID       int
	macvpnAnycastIP    string
	macvpnAnycastMAC   string
	macvpnRouteTargets string
	macvpnARPSuppress  bool
	macvpnDescription  string
)

var evpnMacvpnCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a MAC-VPN definition",
	Long: `Create a MAC-VPN definition in network.json.

This is a spec authoring command that does not require a device connection.

Examples:
  newtron evpn macvpn create servers-vlan100 --vni 1100 --vlan-id 100 -x
  newtron evpn macvpn create servers-vlan100 --vni 1100 --vlan-id 100 --anycast-ip 10.1.100.1/24 --arp-suppress -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if macvpnVNI <= 0 {
			return fmt.Errorf("--vni is required")
		}

		authCtx := auth.NewContext().WithResource(name)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		req := newtron.CreateMACVPNRequest{
			Name:           name,
			VNI:            macvpnVNI,
			VlanID:         macvpnVlanID,
			AnycastIP:      macvpnAnycastIP,
			AnycastMAC:     macvpnAnycastMAC,
			ARPSuppression: macvpnARPSuppress,
			Description:    macvpnDescription,
		}
		if macvpnRouteTargets != "" {
			req.RouteTargets = strings.Split(macvpnRouteTargets, ",")
		}

		fmt.Printf("MAC-VPN: %s\n", name)
		fmt.Printf("  VNI: %d\n", req.VNI)
		if req.VlanID > 0 {
			fmt.Printf("  VLAN ID: %d\n", req.VlanID)
		}
		if req.AnycastIP != "" {
			fmt.Printf("  Anycast IP: %s\n", req.AnycastIP)
		}
		if req.AnycastMAC != "" {
			fmt.Printf("  Anycast MAC: %s\n", req.AnycastMAC)
		}
		if len(req.RouteTargets) > 0 {
			fmt.Printf("  Route Targets: %v\n", req.RouteTargets)
		}
		fmt.Printf("  ARP Suppression: %v\n", req.ARPSuppression)
		if req.Description != "" {
			fmt.Printf("  Description: %s\n", req.Description)
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.CreateMACVPN(req, newtron.ExecOpts{Execute: true}); err != nil {
			return fmt.Errorf("saving MAC-VPN: %w", err)
		}
		fmt.Println("\n" + green("MAC-VPN definition saved to network.json."))

		return nil
	},
}

var evpnMacvpnDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a MAC-VPN definition",
	Long: `Delete a MAC-VPN definition from network.json.

This is a spec authoring command that does not require a device connection.

Examples:
  newtron evpn macvpn delete servers-vlan100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Verify it exists
		if _, err := app.net.ShowMACVPN(name); err != nil {
			return err
		}

		authCtx := auth.NewContext().WithResource(name)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		fmt.Printf("Deleting MAC-VPN: %s\n", name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.DeleteMACVPN(name, newtron.ExecOpts{Execute: true}); err != nil {
			return fmt.Errorf("deleting MAC-VPN: %w", err)
		}
		fmt.Println(green("MAC-VPN definition deleted from network.json."))

		return nil
	},
}

func init() {
	// evpn setup flags
	evpnSetupCmd.Flags().StringVar(&evpnSetupSourceIP, "source-ip", "", "Source IP address for VTEP (defaults to loopback IP)")

	// ipvpn create flags
	evpnIpvpnCreateCmd.Flags().IntVar(&ipvpnL3VNI, "l3vni", 0, "L3VNI for the IP-VPN (required)")
	evpnIpvpnCreateCmd.Flags().StringVar(&ipvpnRouteTargets, "route-targets", "", "Comma-separated route targets")
	evpnIpvpnCreateCmd.Flags().StringVar(&ipvpnVRF, "vrf", "", "VRF name for the IP-VPN")
	evpnIpvpnCreateCmd.Flags().StringVar(&ipvpnDescription, "description", "", "IP-VPN description")

	// macvpn create flags
	evpnMacvpnCreateCmd.Flags().IntVar(&macvpnVNI, "vni", 0, "VNI for the MAC-VPN (required)")
	evpnMacvpnCreateCmd.Flags().IntVar(&macvpnVlanID, "vlan-id", 0, "VLAN ID for the MAC-VPN")
	evpnMacvpnCreateCmd.Flags().StringVar(&macvpnAnycastIP, "anycast-ip", "", "Anycast gateway IP (CIDR)")
	evpnMacvpnCreateCmd.Flags().StringVar(&macvpnAnycastMAC, "anycast-mac", "", "Anycast gateway MAC")
	evpnMacvpnCreateCmd.Flags().StringVar(&macvpnRouteTargets, "route-targets", "", "Comma-separated route targets")
	evpnMacvpnCreateCmd.Flags().BoolVar(&macvpnARPSuppress, "arp-suppress", false, "Enable ARP suppression")
	evpnMacvpnCreateCmd.Flags().StringVar(&macvpnDescription, "description", "", "MAC-VPN description")

	// ipvpn subcommands
	evpnIpvpnCmd.AddCommand(evpnIpvpnListCmd)
	evpnIpvpnCmd.AddCommand(evpnIpvpnShowCmd)
	evpnIpvpnCmd.AddCommand(evpnIpvpnCreateCmd)
	evpnIpvpnCmd.AddCommand(evpnIpvpnDeleteCmd)

	// macvpn subcommands
	evpnMacvpnCmd.AddCommand(evpnMacvpnListCmd)
	evpnMacvpnCmd.AddCommand(evpnMacvpnShowCmd)
	evpnMacvpnCmd.AddCommand(evpnMacvpnCreateCmd)
	evpnMacvpnCmd.AddCommand(evpnMacvpnDeleteCmd)

	// evpn subcommands
	evpnCmd.AddCommand(evpnSetupCmd)
	evpnCmd.AddCommand(evpnStatusCmd)
	evpnCmd.AddCommand(evpnIpvpnCmd)
	evpnCmd.AddCommand(evpnMacvpnCmd)
}
