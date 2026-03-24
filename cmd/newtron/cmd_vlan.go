package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var vlanCmd = &cobra.Command{
	Use:   "vlan",
	Short: "Manage VLANs",
	Long: `Manage VLANs on SONiC devices.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan list
  newtron -D leaf1-ny vlan show 100
  newtron -D leaf1-ny vlan create 100
  newtron -D leaf1-ny vlan add-interface 100 Ethernet0 --tagged
  newtron -D leaf1-ny vlan remove-interface 100 Ethernet0
  newtron -D leaf1-ny vlan status`,
}

var vlanListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all VLANs",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		entries, err := app.client.ListVLANs(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(entries)
		}

		if len(entries) == 0 {
			fmt.Println("No VLANs configured")
			return nil
		}

		sort.Slice(entries, func(i, j int) bool {
			return entries[i].ID < entries[j].ID
		})

		t := cli.NewTable("VLAN ID", "L2VNI", "SVI", "MEMBERS")

		for _, entry := range entries {
			vni := dashInt(entry.L2VNI)
			svi := dash(entry.SVI)
			t.Row(fmt.Sprintf("%d", entry.ID), vni, svi, strings.Join(entry.MemberNames, ","))
		}
		t.Flush()

		return nil
	},
}

var vlanShowCmd = &cobra.Command{
	Use:   "show <vlan-id>",
	Short: "Show detailed VLAN information",
	Long: `Show detailed information about a single VLAN.

Includes VLAN ID, name, members, SVI status, MAC-VPN binding, and L2VNI.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan show 100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}

		if err := requireDevice(); err != nil {
			return err
		}

		entry, err := app.client.ShowVLAN(app.deviceName, vlanID)
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

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		statuses, err := app.client.ListVLANs(app.deviceName)
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

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan create 100 --description "Frontend VLAN"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.CreateVLAN(app.deviceName, vlanID, vlanDescription, execOpts()))
	},
}

var vlanTagged bool

var vlanAddInterfaceCmd = &cobra.Command{
	Use:   "add-interface <vlan-id> <interface>",
	Short: "Add an interface to a VLAN",
	Long: `Add an interface to a VLAN as a tagged or untagged member.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan add-interface 100 Ethernet0 --tagged
  newtron -D leaf1-ny vlan add-interface 100 PortChannel100`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		interfaceName := args[1]
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.AddVLANMember(app.deviceName, vlanID, interfaceName, vlanTagged, execOpts()))
	},
}

var vlanRemoveInterfaceCmd = &cobra.Command{
	Use:   "remove-interface <vlan-id> <interface>",
	Short: "Remove an interface from a VLAN",
	Long: `Remove an interface from a VLAN.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan remove-interface 100 Ethernet0 -x
  newtron -D leaf1-ny vlan remove-interface 100 PortChannel100 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		interfaceName := args[1]
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.RemoveVLANMember(app.deviceName, vlanID, interfaceName, execOpts()))
	},
}

var vlanDeleteCmd = &cobra.Command{
	Use:   "delete <vlan-id>",
	Short: "Delete a VLAN",
	Long: `Delete a VLAN from the device.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan delete 100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.DeleteVLAN(app.deviceName, vlanID, execOpts()))
	},
}

var (
	sviVRF       string
	sviIP        string
	sviAnycastGW string
)

var vlanConfigureIRBCmd = &cobra.Command{
	Use:   "configure-irb <vlan-id>",
	Short: "Configure IRB (Integrated Routing and Bridging) interface",
	Long: `Configure the IRB (Integrated Routing and Bridging) interface for a VLAN.

Creates VLAN_INTERFACE entries for VRF binding and IP address assignment,
and optionally sets up SAG (Static Anycast Gateway) for anycast MAC.

Requires -D (device) flag.

Options:
  --vrf <name>         VRF to bind the IRB to
  --ip <addr/prefix>   IP address with prefix length
  --anycast-gw <mac>   Anycast gateway MAC address (SAG)

Examples:
  newtron -D leaf1-ny vlan configure-irb 100 --vrf Vrf_CUST1 --ip 10.1.100.1/24 -x
  newtron -D leaf1-ny vlan configure-irb 100 --ip 10.1.100.1/24 --anycast-gw 00:00:00:00:01:01 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.ConfigureIRB(app.deviceName, newtron.IRBConfigureRequest{
			VlanID:     vlanID,
			VRF:        sviVRF,
			IPAddress:  sviIP,
			AnycastMAC: sviAnycastGW,
		}, execOpts()))
	},
}

var vlanBindMacvpnCmd = &cobra.Command{
	Use:   "bind-macvpn <vlan-id> <macvpn-name>",
	Short: "Bind a VLAN to a MAC-VPN definition",
	Long: `Bind a VLAN to a MAC-VPN definition from network.json.

The MAC-VPN definition specifies L2VNI and ARP suppression settings.
Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan bind-macvpn 100 servers-vlan100 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		macvpnName := args[1]

		macvpnDef, err := app.client.ShowMACVPN(macvpnName)
		if err != nil {
			return fmt.Errorf("macvpn '%s' not found in network.json", macvpnName)
		}

		if err := requireDevice(); err != nil {
			return err
		}

		vlanIntf := fmt.Sprintf("Vlan%d", vlanID)

		fmt.Printf("MAC-VPN: %s\n", macvpnName)
		fmt.Printf("  VNI: %d\n", macvpnDef.VNI)
		fmt.Printf("  ARP Suppression: %v\n", macvpnDef.ARPSuppression)
		fmt.Println()

		return displayWriteResult(app.client.BindMACVPN(app.deviceName, vlanIntf, macvpnName, execOpts()))
	},
}

var vlanUnbindMacvpnCmd = &cobra.Command{
	Use:   "unbind-macvpn <vlan-id>",
	Short: "Unbind the MAC-VPN from a VLAN",
	Long: `Unbind the MAC-VPN from a VLAN.

Removes the L2VNI mapping and ARP suppression settings.
Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny vlan unbind-macvpn 100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vlanID, err := parseVLANID(args[0])
		if err != nil {
			return err
		}
		if err := requireDevice(); err != nil {
			return err
		}
		vlanIntf := fmt.Sprintf("Vlan%d", vlanID)
		return displayWriteResult(app.client.UnbindMACVPN(app.deviceName, vlanIntf, execOpts()))
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

	vlanConfigureIRBCmd.Flags().StringVar(&sviVRF, "vrf", "", "VRF to bind the IRB to")
	vlanConfigureIRBCmd.Flags().StringVar(&sviIP, "ip", "", "IP address with prefix (e.g., 10.1.100.1/24)")
	vlanConfigureIRBCmd.Flags().StringVar(&sviAnycastGW, "anycast-gw", "", "Anycast gateway MAC (SAG)")

	vlanCmd.AddCommand(vlanListCmd)
	vlanCmd.AddCommand(vlanShowCmd)
	vlanCmd.AddCommand(vlanStatusCmd)
	vlanCmd.AddCommand(vlanCreateCmd)
	vlanCmd.AddCommand(vlanDeleteCmd)
	vlanCmd.AddCommand(vlanAddInterfaceCmd)
	vlanCmd.AddCommand(vlanRemoveInterfaceCmd)
	vlanCmd.AddCommand(vlanConfigureIRBCmd)
	vlanCmd.AddCommand(vlanBindMacvpnCmd)
	vlanCmd.AddCommand(vlanUnbindMacvpnCmd)
}
