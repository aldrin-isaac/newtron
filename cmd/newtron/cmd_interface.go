package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
)

var interfaceCmd = &cobra.Command{
	Use:   "interface",
	Short: "Manage interfaces",
	Long: `Manage device interfaces.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny interface list
  newtron -d leaf1-ny interface show Ethernet0
  newtron -d leaf1-ny interface set Ethernet0 mtu 9000 -x`,
}

var interfaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all interfaces on the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		interfaces := dev.ListInterfaces()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(interfaces)
		}

		if len(interfaces) == 0 {
			fmt.Println("No interfaces found")
			return nil
		}

		t := cli.NewTable("INTERFACE", "ADMIN", "OPER", "IP ADDRESS", "VRF", "SERVICE")

		skipped := 0
		for _, name := range interfaces {
			intf, err := dev.GetInterface(name)
			if err != nil {
				skipped++
				continue
			}

			adminStatus := formatAdminStatus(intf.AdminStatus())
			if adminStatus == "" {
				adminStatus = "-"
			}

			operStatus := formatOperStatus(intf.OperStatus())
			if intf.OperStatus() == "" {
				operStatus = "-"
			}

			ipAddr := "-"
			if addrs := intf.IPAddresses(); len(addrs) > 0 {
				ipAddr = strings.Join(addrs, ",")
			}

			vrf := dash(intf.VRF())

			svc := dash(intf.ServiceName())

			t.Row(name, adminStatus, operStatus, ipAddr, vrf, svc)
		}
		t.Flush()

		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "warning: %d interface(s) could not be read\n", skipped)
		}

		return nil
	},
}

var interfaceShowCmd = &cobra.Command{
	Use:   "show <interface>",
	Short: "Show interface details",
	Long: `Show detailed information about an interface.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny interface show Ethernet0
  newtron -d leaf1-ny interface show Vlan100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		intf, err := dev.GetInterface(intfName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			data := map[string]interface{}{
				"name":         intfName,
				"admin_status": intf.AdminStatus(),
				"oper_status":  intf.OperStatus(),
				"speed":        intf.Speed(),
				"mtu":          intf.MTU(),
				"ip_addresses": intf.IPAddresses(),
				"vrf":          intf.VRF(),
				"service":      intf.ServiceName(),
				"pc_member":    intf.IsPortChannelMember(),
				"pc_parent":    intf.PortChannelParent(),
				"ingress_acl":  intf.IngressACL(),
				"egress_acl":   intf.EgressACL(),
			}
			return json.NewEncoder(os.Stdout).Encode(data)
		}

		fmt.Printf("Interface: %s\n", bold(intfName))

		// Show status with color coding
		adminFmt := formatAdminStatus(intf.AdminStatus())
		if adminFmt == "" {
			adminFmt = "-"
		}
		fmt.Printf("Admin Status: %s\n", adminFmt)

		operFmt := formatOperStatus(intf.OperStatus())
		if intf.OperStatus() == "" {
			operFmt = "-"
		}
		fmt.Printf("Oper Status: %s\n", operFmt)

		fmt.Printf("Speed: %s\n", intf.Speed())
		fmt.Printf("MTU: %d\n", intf.MTU())

		if addrs := intf.IPAddresses(); len(addrs) > 0 {
			fmt.Println("\nIP Addresses:")
			for _, ip := range addrs {
				fmt.Printf("  %s\n", ip)
			}
		}

		if vrf := intf.VRF(); vrf != "" {
			fmt.Printf("\nVRF: %s\n", vrf)
		}

		if svc := intf.ServiceName(); svc != "" {
			fmt.Printf("\nService: %s\n", svc)
		}

		if intf.IsPortChannelMember() {
			fmt.Printf("\nPortChannel Member of: %s\n", intf.PortChannelParent())
		}

		if acl := intf.IngressACL(); acl != "" {
			fmt.Printf("\nIngress ACL: %s\n", acl)
		}
		if acl := intf.EgressACL(); acl != "" {
			fmt.Printf("Egress ACL: %s\n", acl)
		}

		return nil
	},
}

var interfaceGetCmd = &cobra.Command{
	Use:   "get <interface> <property>",
	Short: "Get a specific property value",
	Long: `Get a specific property value from an interface.

Requires -d (device) flag.

Properties:
  mtu           - Interface MTU
  admin-status  - Administrative status (up/down)
  oper-status   - Operational status
  speed         - Interface speed
  description   - Interface description
  vrf           - VRF binding
  ip            - IP addresses

Examples:
  newtron -d leaf1-ny interface get Ethernet0 mtu
  newtron -d leaf1-ny interface get Ethernet0 admin-status
  newtron -d leaf1-ny interface get Ethernet0 vrf`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		property := args[1]
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		intf, err := dev.GetInterface(intfName)
		if err != nil {
			return err
		}

		var value interface{}
		switch property {
		case "mtu":
			value = intf.MTU()
		case "admin-status":
			value = intf.AdminStatus()
		case "oper-status":
			value = intf.OperStatus()
		case "speed":
			value = intf.Speed()
		case "description":
			value = intf.Description()
		case "vrf":
			value = intf.VRF()
		case "ip":
			value = strings.Join(intf.IPAddresses(), ", ")
		default:
			return fmt.Errorf("unknown property: %s", property)
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{property: value})
		}
		fmt.Println(value)
		return nil
	},
}

var interfaceSetCmd = &cobra.Command{
	Use:   "set <interface> <property> <value>",
	Short: "Set a property on an interface",
	Long: `Set a property on an interface.

Requires -d (device) flag.

Properties:
  mtu <value>           - Interface MTU
  admin-status <up|down> - Administrative status
  description <text>    - Interface description
  vrf <name>            - VRF binding
  ip <address/prefix>   - IP address

Examples:
  newtron -d leaf1-ny interface set Ethernet0 mtu 9000 -x
  newtron -d leaf1-ny interface set Ethernet0 admin-status down -x
  newtron -d leaf1-ny interface set Ethernet0 description "Uplink to spine" -x`,
	Args: cobra.MinimumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		property := args[1]
		value := strings.Join(args[2:], " ")
		return withDeviceWrite(func(ctx context.Context, dev *node.Node) (*node.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(intfName)
			if err := checkExecutePermission(auth.PermInterfaceModify, authCtx); err != nil {
				return nil, err
			}
			intf, err := dev.GetInterface(intfName)
			if err != nil {
				return nil, err
			}
			cs, err := intf.Set(ctx, property, value)
			if err != nil {
				return nil, fmt.Errorf("setting %s: %w", property, err)
			}
			return cs, nil
		})
	},
}

var interfaceListAclsCmd = &cobra.Command{
	Use:   "list-acls <interface>",
	Short: "List ACLs bound to an interface",
	Long: `List ACLs bound to an interface.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny interface list-acls Ethernet0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		intf, err := dev.GetInterface(intfName)
		if err != nil {
			return err
		}

		ingressACL := intf.IngressACL()
		egressACL := intf.EgressACL()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"ingress": ingressACL,
				"egress":  egressACL,
			})
		}

		if ingressACL == "" && egressACL == "" {
			fmt.Println("(no ACLs bound)")
			return nil
		}

		if ingressACL != "" {
			fmt.Printf("Ingress: %s\n", ingressACL)
		}
		if egressACL != "" {
			fmt.Printf("Egress: %s\n", egressACL)
		}
		return nil
	},
}

var interfaceListMembersCmd = &cobra.Command{
	Use:   "list-members <interface>",
	Short: "List members of a LAG or VLAN",
	Long: `List members of a LAG (PortChannel) or VLAN.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny interface list-members PortChannel100
  newtron -d leaf1-ny interface list-members Vlan100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		intf, err := dev.GetInterface(intfName)
		if err != nil {
			return err
		}

		// Check if it's a PortChannel
		if members := intf.PortChannelMembers(); len(members) > 0 {
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(members)
			}
			fmt.Println("LAG Members:")
			for _, m := range members {
				fmt.Printf("  %s\n", m)
			}
			return nil
		}

		// Check if it's a VLAN
		if members := intf.VLANMembers(); len(members) > 0 {
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(members)
			}
			fmt.Println("VLAN Members:")
			for _, m := range members {
				fmt.Printf("  %s\n", m)
			}
			return nil
		}

		fmt.Println("(no members)")
		return nil
	},
}

func init() {
	interfaceCmd.AddCommand(interfaceListCmd)
	interfaceCmd.AddCommand(interfaceShowCmd)
	interfaceCmd.AddCommand(interfaceGetCmd)
	interfaceCmd.AddCommand(interfaceSetCmd)
	interfaceCmd.AddCommand(interfaceListAclsCmd)
	interfaceCmd.AddCommand(interfaceListMembersCmd)
}
