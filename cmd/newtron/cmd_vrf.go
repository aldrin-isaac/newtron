package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/network"
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
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		vrfNames := dev.ListVRFs()

		if app.jsonOutput {
			var vrfs []*network.VRFInfo
			skipped := 0
			for _, name := range vrfNames {
				vrf, err := dev.GetVRF(name)
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
			vrf, err := dev.GetVRF(name)
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
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		vrf, err := dev.GetVRF(vrfName)
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
		configDB := dev.ConfigDB()
		if configDB != nil && len(configDB.BGPNeighbor) > 0 {
			vrfPrefix := vrfName + "|"
			hasNeighbors := false
			for key, neighbor := range configDB.BGPNeighbor {
				if strings.HasPrefix(key, vrfPrefix) {
					if !hasNeighbors {
						fmt.Println("\nBGP Neighbors:")
						hasNeighbors = true
					}
					// Extract neighbor IP from key "Vrf_CUST1|10.1.1.2"
					parts := strings.SplitN(key, "|", 2)
					neighborIP := parts[1]
					desc := dash(neighbor.Name)
					fmt.Printf("  %s  AS %s  %s\n", neighborIP, neighbor.ASN, desc)
				}
			}
			if !hasNeighbors {
				fmt.Println("\nBGP Neighbors: (none)")
			}
		}

		return nil
	},
}

var vrfStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show VRF config and operational state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		vrfNames := dev.ListVRFs()

		if len(vrfNames) == 0 {
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode([]struct{}{})
			}
			fmt.Println("No VRFs configured")
			return nil
		}

		underlying := dev.Underlying()

		type vrfStatusEntry struct {
			Name       string `json:"name"`
			L3VNI      int    `json:"l3_vni,omitempty"`
			Interfaces int    `json:"interfaces"`
			State      string `json:"state,omitempty"`
			RouteCount int    `json:"route_count,omitempty"`
		}

		var statuses []vrfStatusEntry
		for _, name := range vrfNames {
			vrf, err := dev.GetVRF(name)
			if err != nil {
				continue
			}
			s := vrfStatusEntry{
				Name:       name,
				L3VNI:      vrf.L3VNI,
				Interfaces: len(vrf.Interfaces),
			}
			if underlying != nil && underlying.State != nil {
				if vrfState, ok := underlying.State.VRFs[name]; ok {
					s.State = vrfState.State
					s.RouteCount = vrfState.RouteCount
				}
			}
			statuses = append(statuses, s)
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(statuses)
		}

		fmt.Printf("VRF Status for %s\n\n", bold(app.deviceName))

		// Config view
		t := cli.NewTable("VRF", "L3VNI", "INTERFACES", "STATE", "ROUTES")

		for _, s := range statuses {
			l3vni := dashInt(s.L3VNI)

			intfCount := fmt.Sprintf("%d", s.Interfaces)

			state := "-"
			if s.State != "" {
				state = formatOperStatus(s.State)
			}
			routeCount := dashInt(s.RouteCount)

			t.Row(s.Name, l3vni, intfCount, state, routeCount)
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFCreate, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.CreateVRF(ctx, vrfName, network.VRFConfig{})
			if err != nil {
				return nil, fmt.Errorf("creating VRF: %w", err)
			}
			return cs, nil
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFDelete, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.DeleteVRF(ctx, vrfName)
			if err != nil {
				return nil, fmt.Errorf("deleting VRF: %w", err)
			}
			return cs, nil
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.AddVRFInterface(ctx, vrfName, intfName)
			if err != nil {
				return nil, fmt.Errorf("adding interface to VRF: %w", err)
			}
			return cs, nil
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.RemoveVRFInterface(ctx, vrfName, intfName)
			if err != nil {
				return nil, fmt.Errorf("removing interface from VRF: %w", err)
			}
			return cs, nil
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

		ipvpnDef, err := app.net.GetIPVPN(ipvpnName)
		if err != nil {
			return fmt.Errorf("ipvpn '%s' not found in network.json", ipvpnName)
		}

		fmt.Printf("IP-VPN: %s\n", ipvpnName)
		fmt.Printf("  L3VNI: %d\n", ipvpnDef.L3VNI)
		if len(ipvpnDef.ImportRT) > 0 {
			fmt.Printf("  Import RT: %v\n", ipvpnDef.ImportRT)
		}
		if len(ipvpnDef.ExportRT) > 0 {
			fmt.Printf("  Export RT: %v\n", ipvpnDef.ExportRT)
		}
		fmt.Println()

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.BindIPVPN(ctx, vrfName, ipvpnDef)
			if err != nil {
				return nil, fmt.Errorf("binding IP-VPN: %w", err)
			}
			return cs, nil
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.UnbindIPVPN(ctx, vrfName)
			if err != nil {
				return nil, fmt.Errorf("unbinding IP-VPN: %w", err)
			}
			return cs, nil
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

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}
			intf, err := dev.GetInterface(intfName)
			if err != nil {
				return nil, err
			}

			// Verify the interface belongs to the specified VRF
			if intf.VRF() != vrfName {
				return nil, fmt.Errorf("interface %s is not in VRF %s (current VRF: %q)", intfName, vrfName, intf.VRF())
			}

			cs, err := intf.AddBGPNeighbor(ctx, network.DirectBGPNeighborConfig{
				NeighborIP:  vrfNeighborIP,
				RemoteAS:    asn,
				Description: vrfNeighborDescription,
			})
			if err != nil {
				return nil, fmt.Errorf("adding BGP neighbor: %w", err)
			}
			return cs, nil
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

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}

			// Try as interface first
			intf, intfErr := dev.GetInterface(target)
			if intfErr == nil {
				// It's an interface — remove its BGP neighbor
				cs, err := intf.RemoveBGPNeighbor(ctx, "")
				if err != nil {
					return nil, fmt.Errorf("removing BGP neighbor from interface: %w", err)
				}
				return cs, nil
			}

			// Not an interface — treat as neighbor IP
			cs, err := dev.RemoveBGPNeighbor(ctx, target)
			if err != nil {
				return nil, fmt.Errorf("removing BGP neighbor: %w", err)
			}
			return cs, nil
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

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.AddStaticRoute(ctx, vrfName, prefix, nextHop, vrfRouteMetric)
			if err != nil {
				return nil, fmt.Errorf("adding static route: %w", err)
			}
			return cs, nil
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

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(vrfName)
			if err := checkExecutePermission(auth.PermVRFModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.RemoveStaticRoute(ctx, vrfName, prefix)
			if err != nil {
				return nil, fmt.Errorf("removing static route: %w", err)
			}
			return cs, nil
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
