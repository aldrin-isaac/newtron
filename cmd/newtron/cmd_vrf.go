package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var vrfCmd = &cobra.Command{
	Use:   "vrf",
	Short: "Manage VRFs (Virtual Routing and Forwarding)",
	Long: `Manage VRFs on SONiC devices.

VRF is a first-class routing context that owns interfaces, BGP neighbors,
IP-VPN binding, and static routes.

Requires -d (device) flag for all commands.

Examples:
  newtron leaf1 vrf list
  newtron leaf1 vrf show Vrf_CUST1
  newtron leaf1 vrf status
  newtron leaf1 vrf create Vrf_CUST1 -x
  newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet4 -x
  newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x
  newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet4 65200 -x`,
}

var vrfListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all VRFs",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		vrfNames := n.ListVRFs()

		if app.jsonOutput {
			var vrfs []*newtron.VRFDetail
			skipped := 0
			for _, name := range vrfNames {
				vrf, err := n.ShowVRF(name)
				if err != nil {
					skipped++
					continue
				}
				vrfs = append(vrfs, vrf)
			}
			if err := json.NewEncoder(os.Stdout).Encode(vrfs); err != nil {
				return err
			}
			if skipped > 0 {
				fmt.Fprintf(os.Stderr, "warning: %d VRF(s) could not be read\n", skipped)
			}
			return nil
		}

		if len(vrfNames) == 0 {
			fmt.Println("No VRFs configured")
			return nil
		}

		t := cli.NewTable("NAME", "L3VNI", "INTERFACES")

		skipped := 0
		for _, name := range vrfNames {
			vrf, err := n.ShowVRF(name)
			if err != nil {
				skipped++
				continue
			}

			l3vni := dashInt(vrf.L3VNI)

			intfs := "-"
			if len(vrf.Interfaces) > 0 {
				intfs = strings.Join(vrf.Interfaces, ",")
			}

			t.Row(name, l3vni, intfs)
		}
		t.Flush()

		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "warning: %d VRF(s) could not be read\n", skipped)
		}

		return nil
	},
}

var vrfShowCmd = &cobra.Command{
	Use:   "show <vrf-name>",
	Short: "Show VRF details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]

		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		vrf, err := n.ShowVRF(vrfName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(vrf)
		}

		fmt.Printf("VRF: %s\n", bold(vrf.Name))

		l3vni := "(none)"
		if vrf.L3VNI > 0 {
			l3vni = fmt.Sprintf("%d", vrf.L3VNI)
		}
		fmt.Printf("L3VNI: %s\n", l3vni)

		if len(vrf.Interfaces) > 0 {
			fmt.Printf("\nInterfaces (%d):\n", len(vrf.Interfaces))
			for _, intf := range vrf.Interfaces {
				fmt.Printf("  %s\n", intf)
			}
		} else {
			fmt.Println("\nInterfaces: (none)")
		}

		// Show BGP neighbors in this VRF
		if len(vrf.BGPNeighbors) > 0 {
			fmt.Println("\nBGP Neighbors:")
			for _, neighbor := range vrf.BGPNeighbors {
				desc := dash(neighbor.Description)
				fmt.Printf("  %s  AS %s  %s\n", neighbor.Address, neighbor.ASN, desc)
			}
		} else {
			fmt.Println("\nBGP Neighbors: (none)")
		}

		return nil
	},
}

var vrfStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show VRF config and operational state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		statuses, err := n.VRFStatus()
		if err != nil {
			return err
		}

		if len(statuses) == 0 {
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode([]struct{}{})
			}
			fmt.Println("No VRFs configured")
			return nil
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(statuses)
		}

		fmt.Printf("VRF Status for %s\n\n", bold(app.deviceName))

		t := cli.NewTable("VRF", "L3VNI", "INTERFACES", "STATE", "ROUTES")

		for _, s := range statuses {
			l3vni := dashInt(s.L3VNI)

			intfCount := fmt.Sprintf("%d", s.Interfaces)

			state := "-"
			if s.State != "" {
				state = formatOperStatus(s.State)
			}

			t.Row(s.Name, l3vni, intfCount, state, "-")
		}
		t.Flush()

		return nil
	},
}

var vrfCreateCmd = &cobra.Command{
	Use:   "create <vrf-name>",
	Short: "Create a new VRF",
	Long: `Create a new VRF on the device.

The VRF is created without an L3VNI. Use 'vrf bind-ipvpn' to associate
it with an IP-VPN definition which provides L3VNI and route targets.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf create Vrf_CUST1 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFCreate, authCtx); err != nil {
				return err
			}
			if err := n.CreateVRF(ctx, vrfName, newtron.VRFConfig{Name: vrfName}); err != nil {
				return fmt.Errorf("creating VRF: %w", err)
			}
			return nil
		})
	},
}

var vrfDeleteCmd = &cobra.Command{
	Use:   "delete <vrf-name>",
	Short: "Delete a VRF",
	Long: `Delete a VRF from the device.

The VRF must have no interfaces bound before it can be deleted.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf delete Vrf_CUST1 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFDelete, authCtx); err != nil {
				return err
			}
			if err := n.DeleteVRF(ctx, vrfName); err != nil {
				return fmt.Errorf("deleting VRF: %w", err)
			}
			return nil
		})
	},
}

var vrfAddInterfaceCmd = &cobra.Command{
	Use:   "add-interface <vrf-name> <interface>",
	Short: "Add an interface to a VRF",
	Long: `Bind an interface to a VRF.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf add-interface Vrf_CUST1 Ethernet4 -x
  newtron leaf1 vrf add-interface Vrf_CUST1 Vlan100 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		intfName := args[1]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}
			if err := n.AddVRFInterface(ctx, vrfName, intfName); err != nil {
				return fmt.Errorf("adding interface to VRF: %w", err)
			}
			return nil
		})
	},
}

var vrfRemoveInterfaceCmd = &cobra.Command{
	Use:   "remove-interface <vrf-name> <interface>",
	Short: "Remove an interface from a VRF",
	Long: `Unbind an interface from a VRF.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf remove-interface Vrf_CUST1 Ethernet4 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		intfName := args[1]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}
			if err := n.RemoveVRFInterface(ctx, vrfName, intfName); err != nil {
				return fmt.Errorf("removing interface from VRF: %w", err)
			}
			return nil
		})
	},
}

var vrfBindIPVPNCmd = &cobra.Command{
	Use:   "bind-ipvpn <vrf-name> <ipvpn-name>",
	Short: "Bind a VRF to an IP-VPN definition",
	Long: `Bind a VRF to an IP-VPN definition from network.json.

The IP-VPN definition provides L3VNI and route targets for the VRF.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf bind-ipvpn Vrf_CUST1 customer-vpn -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		ipvpnName := args[1]

		ipvpnDef, err := app.net.ShowIPVPN(ipvpnName)
		if err != nil {
			return fmt.Errorf("ipvpn '%s' not found in network.json", ipvpnName)
		}

		fmt.Printf("IP-VPN: %s\n", ipvpnName)
		fmt.Printf("  L3VNI: %d\n", ipvpnDef.L3VNI)
		if ipvpnDef.VRF != "" {
			fmt.Printf("  VRF: %s\n", ipvpnDef.VRF)
		}
		if len(ipvpnDef.RouteTargets) > 0 {
			fmt.Printf("  Route Targets: %v\n", ipvpnDef.RouteTargets)
		}
		fmt.Println()

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}
			if err := n.BindIPVPN(ctx, vrfName, ipvpnName); err != nil {
				return fmt.Errorf("binding IP-VPN: %w", err)
			}
			return nil
		})
	},
}

var vrfUnbindIPVPNCmd = &cobra.Command{
	Use:   "unbind-ipvpn <vrf-name>",
	Short: "Unbind the IP-VPN from a VRF",
	Long: `Unbind the IP-VPN from a VRF, removing L3VNI and route targets.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf unbind-ipvpn Vrf_CUST1 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}
			if err := n.UnbindIPVPN(ctx, vrfName); err != nil {
				return fmt.Errorf("unbinding IP-VPN: %w", err)
			}
			return nil
		})
	},
}

var (
	vrfNeighborIP          string
	vrfNeighborDescription string
)

var vrfAddNeighborCmd = &cobra.Command{
	Use:   "add-neighbor <vrf-name> <interface> <remote-asn>",
	Short: "Add a BGP neighbor to a VRF interface",
	Long: `Add a direct BGP neighbor on a VRF interface.

The neighbor IP is auto-derived for /30 and /31 subnets unless --neighbor is specified.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet4 65200 -x
  newtron leaf1 vrf add-neighbor Vrf_CUST1 Ethernet4 65200 --neighbor 10.1.1.2 --description "customer-a" -x`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		intfName := args[1]
		asn, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("invalid ASN: %s", args[2])
		}

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}
			iface, err := n.Interface(intfName)
			if err != nil {
				return err
			}

			// Verify the interface belongs to the specified VRF
			if iface.VRF() != vrfName {
				return fmt.Errorf("interface %s is not in VRF %s (current VRF: %q)", intfName, vrfName, iface.VRF())
			}

			if err := iface.AddBGPNeighbor(ctx, newtron.BGPNeighborConfig{
				VRF:         vrfName,
				Interface:   intfName,
				NeighborIP:  vrfNeighborIP,
				RemoteAS:    asn,
				Description: vrfNeighborDescription,
			}); err != nil {
				return fmt.Errorf("adding BGP neighbor: %w", err)
			}
			return nil
		})
	},
}

var vrfRemoveNeighborCmd = &cobra.Command{
	Use:   "remove-neighbor <vrf-name> <interface|ip>",
	Short: "Remove a BGP neighbor from a VRF",
	Long: `Remove a BGP neighbor from a VRF.

The second argument can be an interface name (removes the auto-derived neighbor)
or a neighbor IP address (removes that specific neighbor).

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf remove-neighbor Vrf_CUST1 Ethernet4 -x
  newtron leaf1 vrf remove-neighbor Vrf_CUST1 10.1.1.2 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		target := args[1]

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}

			// Try as interface first
			iface, intfErr := n.Interface(target)
			if intfErr == nil {
				// It's an interface — remove its BGP neighbor
				if err := iface.RemoveBGPNeighbor(ctx, ""); err != nil {
					return fmt.Errorf("removing BGP neighbor from interface: %w", err)
				}
				return nil
			}

			// Not an interface — treat as neighbor IP
			if err := n.RemoveBGPNeighbor(ctx, target); err != nil {
				return fmt.Errorf("removing BGP neighbor: %w", err)
			}
			return nil
		})
	},
}

var vrfRouteMetric int

var vrfAddRouteCmd = &cobra.Command{
	Use:   "add-route <vrf-name> <prefix> <next-hop>",
	Short: "Add a static route to a VRF",
	Long: `Add a static route to a VRF routing table.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf add-route Vrf_CUST1 10.0.0.0/8 10.1.1.1 -x
  newtron leaf1 vrf add-route Vrf_CUST1 0.0.0.0/0 10.1.1.1 --metric 100 -x`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		prefix := args[1]
		nextHop := args[2]

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}
			if err := n.AddStaticRoute(ctx, vrfName, prefix, nextHop, vrfRouteMetric); err != nil {
				return fmt.Errorf("adding static route: %w", err)
			}
			return nil
		})
	},
}

var vrfRemoveRouteCmd = &cobra.Command{
	Use:   "remove-route <vrf-name> <prefix>",
	Short: "Remove a static route from a VRF",
	Long: `Remove a static route from a VRF routing table.

Requires -d (device) flag.

Examples:
  newtron leaf1 vrf remove-route Vrf_CUST1 10.0.0.0/8 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		prefix := args[1]

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return err
			}
			if err := n.RemoveStaticRoute(ctx, vrfName, prefix); err != nil {
				return fmt.Errorf("removing static route: %w", err)
			}
			return nil
		})
	},
}

func init() {
	vrfAddNeighborCmd.Flags().StringVar(&vrfNeighborIP, "neighbor", "", "Neighbor IP (auto-derived for /30, /31 if not specified)")
	vrfAddNeighborCmd.Flags().StringVar(&vrfNeighborDescription, "description", "", "Neighbor description")

	vrfAddRouteCmd.Flags().IntVar(&vrfRouteMetric, "metric", 0, "Route metric")

	vrfCmd.AddCommand(vrfListCmd)
	vrfCmd.AddCommand(vrfShowCmd)
	vrfCmd.AddCommand(vrfStatusCmd)
	vrfCmd.AddCommand(vrfCreateCmd)
	vrfCmd.AddCommand(vrfDeleteCmd)
	vrfCmd.AddCommand(vrfAddInterfaceCmd)
	vrfCmd.AddCommand(vrfRemoveInterfaceCmd)
	vrfCmd.AddCommand(vrfBindIPVPNCmd)
	vrfCmd.AddCommand(vrfUnbindIPVPNCmd)
	vrfCmd.AddCommand(vrfAddNeighborCmd)
	vrfCmd.AddCommand(vrfRemoveNeighborCmd)
	vrfCmd.AddCommand(vrfAddRouteCmd)
	vrfCmd.AddCommand(vrfRemoveRouteCmd)
}
