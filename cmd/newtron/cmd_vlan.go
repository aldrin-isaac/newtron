package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var vlanCmd = &cobra.Command{
	Use:   "vlan",
	Short: "Manage VLANs",
	Long: `Manage VLANs on SONiC devices.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan list
  newtron -d leaf1-ny vlan show 100
  newtron -d leaf1-ny vlan create 100
  newtron -d leaf1-ny vlan add-interface 100 Ethernet0 --tagged
  newtron -d leaf1-ny vlan remove-interface 100 Ethernet0
  newtron -d leaf1-ny vlan status`,
}

var vlanListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all VLANs",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		vlanIDs := n.ListVLANs()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(vlanIDs)
		}

		if len(vlanIDs) == 0 {
			fmt.Println("No VLANs configured")
			return nil
		}

		sort.Ints(vlanIDs)

		t := cli.NewTable("VLAN ID", "L2VNI", "SVI", "MEMBERS")

		skipped := 0
		for _, id := range vlanIDs {
			entry, err := n.ShowVLAN(id)
			if err != nil {
				skipped++
				continue
			}

			vni := dashInt(entry.L2VNI)

			svi := dash(entry.SVI)

			t.Row(fmt.Sprintf("%d", entry.ID), vni, svi, strings.Join(entry.MemberNames, ","))
		}
		t.Flush()

		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "warning: %d VLAN(s) could not be read\n", skipped)
		}

		return nil
	},
}

var vlanShowCmd = &cobra.Command{
	Use:   "show <vlan-id>",
	Short: "Show detailed VLAN information",
	Long: `Show detailed information about a single VLAN.

Includes VLAN ID, name, members, SVI status, MAC-VPN binding, and L2VNI.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan show 100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}

		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		entry, err := n.ShowVLAN(vlanID)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(entry)
		}

		fmt.Printf("VLAN: %s\n", bold(fmt.Sprintf("Vlan%d", vlanID)))
		if entry.Name != "" {
			fmt.Printf("Name: %s\n", entry.Name)
		}

		if len(entry.MemberNames) > 0 {
			fmt.Printf("Members: %s\n", strings.Join(entry.MemberNames, ", "))
		} else {
			fmt.Println("Members: (none)")
		}

		if entry.SVI != "" {
			fmt.Printf("SVI: %s\n", green(entry.SVI))
		} else {
			fmt.Println("SVI: (not configured)")
		}

		// MAC-VPN / L2VNI info
		if entry.MACVPNInfo != nil {
			fmt.Println("\nMAC-VPN Binding:")
			if entry.MACVPNInfo.Name != "" {
				fmt.Printf("  MAC-VPN: %s\n", entry.MACVPNInfo.Name)
			}
			if entry.MACVPNInfo.L2VNI > 0 {
				fmt.Printf("  L2VNI: %d\n", entry.MACVPNInfo.L2VNI)
			}
			fmt.Printf("  ARP Suppression: %v\n", entry.MACVPNInfo.ARPSuppression)
		} else {
			fmt.Println("\nMAC-VPN: (not bound)")
		}

		return nil
	},
}

var vlanStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all VLANs with operational state",
	Long: `Show all VLANs with their operational state from STATE_DB.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		statuses, err := n.VLANStatus()
		if err != nil {
			return err
		}

		if len(statuses) == 0 {
			fmt.Println("No VLANs configured")
			return nil
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(statuses)
		}

		t := cli.NewTable("VLAN ID", "NAME", "L2VNI", "SVI", "MEMBERS", "MAC-VPN")

		for _, s := range statuses {
			vni := dashInt(s.L2VNI)
			svi := "-"
			if s.SVI != "" {
				svi = green(s.SVI)
			}
			name := dash(s.Name)
			macvpn := dash(s.MACVPN)
			t.Row(fmt.Sprintf("%d", s.ID), name, vni, svi, fmt.Sprintf("%d", s.MemberCount), macvpn)
		}
		t.Flush()

		return nil
	},
}

var (
	vlanDescription string
)

var vlanCreateCmd = &cobra.Command{
	Use:   "create <vlan-id>",
	Short: "Create a new VLAN",
	Long: `Create a new VLAN on the device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan create 100 --description "Frontend VLAN"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
			if err := checkExecutePermission(auth.PermVLANCreate, authCtx); err != nil {
				return err
			}
			if err := n.CreateVLAN(ctx, vlanID, newtron.VLANConfig{
				Description: vlanDescription,
			}); err != nil {
				return fmt.Errorf("creating VLAN: %w", err)
			}
			return nil
		})
	},
}

var vlanTagged bool

var vlanAddInterfaceCmd = &cobra.Command{
	Use:   "add-interface <vlan-id> <interface>",
	Short: "Add an interface to a VLAN",
	Long: `Add an interface to a VLAN as a tagged or untagged member.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan add-interface 100 Ethernet0 --tagged
  newtron -d leaf1-ny vlan add-interface 100 PortChannel100`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		interfaceName := args[1]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
			if err := checkExecutePermission(auth.PermVLANModify, authCtx); err != nil {
				return err
			}
			if err := n.AddVLANMember(ctx, vlanID, interfaceName, vlanTagged); err != nil {
				return fmt.Errorf("adding interface: %w", err)
			}
			return nil
		})
	},
}

var vlanRemoveInterfaceCmd = &cobra.Command{
	Use:   "remove-interface <vlan-id> <interface>",
	Short: "Remove an interface from a VLAN",
	Long: `Remove an interface from a VLAN.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan remove-interface 100 Ethernet0 -x
  newtron -d leaf1-ny vlan remove-interface 100 PortChannel100 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		interfaceName := args[1]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
			if err := checkExecutePermission(auth.PermVLANModify, authCtx); err != nil {
				return err
			}
			if err := n.RemoveVLANMember(ctx, vlanID, interfaceName); err != nil {
				return fmt.Errorf("removing interface: %w", err)
			}
			return nil
		})
	},
}

var vlanDeleteCmd = &cobra.Command{
	Use:   "delete <vlan-id>",
	Short: "Delete a VLAN",
	Long: `Delete a VLAN from the device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan delete 100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
			if err := checkExecutePermission(auth.PermVLANDelete, authCtx); err != nil {
				return err
			}
			if err := n.DeleteVLAN(ctx, vlanID); err != nil {
				return fmt.Errorf("deleting VLAN: %w", err)
			}
			return nil
		})
	},
}

var (
	sviVRF       string
	sviIP        string
	sviAnycastGW string
)

var vlanConfigureSVICmd = &cobra.Command{
	Use:   "configure-svi <vlan-id>",
	Short: "Configure SVI (Layer 3 VLAN interface)",
	Long: `Configure the SVI (Switched Virtual Interface) for a VLAN.

Creates VLAN_INTERFACE entries for VRF binding and IP address assignment,
and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.

Requires -d (device) flag.

Options:
  --vrf <name>         VRF to bind the SVI to
  --ip <addr/prefix>   IP address with prefix length
  --anycast-gw <mac>   Anycast gateway MAC address (SAG)

Examples:
  newtron -d leaf1-ny vlan configure-svi 100 --vrf Vrf_CUST1 --ip 10.1.100.1/24 -x
  newtron -d leaf1-ny vlan configure-svi 100 --ip 10.1.100.1/24 --anycast-gw 00:00:00:00:01:01 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
			if err := checkExecutePermission(auth.PermInterfaceModify, authCtx); err != nil {
				return err
			}
			if err := n.ConfigureSVI(ctx, vlanID, newtron.SVIConfig{
				VRF:        sviVRF,
				IPAddress:  sviIP,
				AnycastMAC: sviAnycastGW,
			}); err != nil {
				return fmt.Errorf("configuring SVI: %w", err)
			}
			return nil
		})
	},
}

var vlanBindMacvpnCmd = &cobra.Command{
	Use:   "bind-macvpn <vlan-id> <macvpn-name>",
	Short: "Bind a VLAN to a MAC-VPN definition",
	Long: `Bind a VLAN to a MAC-VPN definition from network.json.

The MAC-VPN definition specifies L2VNI and ARP suppression settings.
Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan bind-macvpn 100 servers-vlan100 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		macvpnName := args[1]

		macvpnDef, err := app.net.ShowMACVPN(macvpnName)
		if err != nil {
			return fmt.Errorf("macvpn '%s' not found in network.json", macvpnName)
		}

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
			if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
				return err
			}

			vlanIntf := fmt.Sprintf("Vlan%d", vlanID)
			iface, err := n.Interface(vlanIntf)
			if err != nil {
				return fmt.Errorf("VLAN %d not found", vlanID)
			}

			fmt.Printf("MAC-VPN: %s\n", macvpnName)
			fmt.Printf("  VNI: %d\n", macvpnDef.VNI)
			fmt.Printf("  ARP Suppression: %v\n", macvpnDef.ARPSuppression)
			fmt.Println()

			if err := iface.BindMACVPN(ctx, macvpnName); err != nil {
				return fmt.Errorf("binding MAC-VPN: %w", err)
			}
			return nil
		})
	},
}

var vlanUnbindMacvpnCmd = &cobra.Command{
	Use:   "unbind-macvpn <vlan-id>",
	Short: "Unbind the MAC-VPN from a VLAN",
	Long: `Unbind the MAC-VPN from a VLAN.

Removes the L2VNI mapping and ARP suppression settings.
Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan unbind-macvpn 100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
			if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
				return err
			}

			vlanIntf := fmt.Sprintf("Vlan%d", vlanID)
			iface, err := n.Interface(vlanIntf)
			if err != nil {
				return fmt.Errorf("VLAN %d not found", vlanID)
			}

			if err := iface.UnbindMACVPN(ctx); err != nil {
				return fmt.Errorf("unbinding MAC-VPN: %w", err)
			}
			return nil
		})
	},
}

// parseVLANID parses a VLAN ID from a string argument.
func parseVLANID(s string) (int, error) {
	var id int
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
		return 0, fmt.Errorf("invalid VLAN ID: %s", s)
	}
	return id, nil
}

func init() {
	vlanCreateCmd.Flags().StringVar(&vlanDescription, "description", "", "VLAN description")

	vlanAddInterfaceCmd.Flags().BoolVar(&vlanTagged, "tagged", false, "Add as tagged member")

	vlanConfigureSVICmd.Flags().StringVar(&sviVRF, "vrf", "", "VRF to bind the SVI to")
	vlanConfigureSVICmd.Flags().StringVar(&sviIP, "ip", "", "IP address with prefix (e.g., 10.1.100.1/24)")
	vlanConfigureSVICmd.Flags().StringVar(&sviAnycastGW, "anycast-gw", "", "Anycast gateway MAC (SAG)")

	vlanCmd.AddCommand(vlanListCmd)
	vlanCmd.AddCommand(vlanShowCmd)
	vlanCmd.AddCommand(vlanStatusCmd)
	vlanCmd.AddCommand(vlanCreateCmd)
	vlanCmd.AddCommand(vlanDeleteCmd)
	vlanCmd.AddCommand(vlanAddInterfaceCmd)
	vlanCmd.AddCommand(vlanRemoveInterfaceCmd)
	vlanCmd.AddCommand(vlanConfigureSVICmd)
	vlanCmd.AddCommand(vlanBindMacvpnCmd)
	vlanCmd.AddCommand(vlanUnbindMacvpnCmd)
}
