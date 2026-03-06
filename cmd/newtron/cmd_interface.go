package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
)

var interfaceCmd = &cobra.Command{
	Use:   "interface",
	Short: "Manage interfaces",
	Long: `Manage device interfaces.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny interface list
  newtron -D leaf1-ny interface show Ethernet0
  newtron -D leaf1-ny interface set Ethernet0 mtu 9000 -x`,
}

var interfaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all interfaces on the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		interfaces, err := app.client.ListInterfaces(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(interfaces)
		}

		if len(interfaces) == 0 {
			fmt.Println("No interfaces found")
			return nil
		}

		t := cli.NewTable("INTERFACE", "ADMIN", "OPER", "IP ADDRESS", "VRF", "SERVICE")

		for _, intf := range interfaces {
			adminStatus := formatAdminStatus(intf.AdminStatus)
			if adminStatus == "" {
				adminStatus = "-"
			}

			operStatus := formatOperStatus(intf.OperStatus)
			if intf.OperStatus == "" {
				operStatus = "-"
			}

			ipAddr := "-"
			if len(intf.IPAddresses) > 0 {
				ipAddr = strings.Join(intf.IPAddresses, ",")
			}

			vrf := dash(intf.VRF)
			svc := dash(intf.Service)

			t.Row(intf.Name, adminStatus, operStatus, ipAddr, vrf, svc)
		}
		t.Flush()

		return nil
	},
}

var interfaceShowCmd = &cobra.Command{
	Use:   "show <interface>",
	Short: "Show interface details",
	Long: `Show detailed information about an interface.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny interface show Ethernet0
  newtron -D leaf1-ny interface show Vlan100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		if err := requireDevice(); err != nil {
			return err
		}

		detail, err := app.client.ShowInterface(app.deviceName, intfName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			data := map[string]interface{}{
				"name":         detail.Name,
				"admin_status": detail.AdminStatus,
				"oper_status":  detail.OperStatus,
				"speed":        detail.Speed,
				"mtu":          detail.MTU,
				"ip_addresses": detail.IPAddresses,
				"vrf":          detail.VRF,
				"service":      detail.Service,
				"pc_member":    detail.PCMember,
				"pc_parent":    detail.PCParent,
				"ingress_acl":  detail.IngressACL,
				"egress_acl":   detail.EgressACL,
			}
			return json.NewEncoder(os.Stdout).Encode(data)
		}

		fmt.Printf("Interface: %s\n", bold(intfName))

		// Show status with color coding
		adminFmt := formatAdminStatus(detail.AdminStatus)
		if adminFmt == "" {
			adminFmt = "-"
		}
		fmt.Printf("Admin Status: %s\n", adminFmt)

		operFmt := formatOperStatus(detail.OperStatus)
		if detail.OperStatus == "" {
			operFmt = "-"
		}
		fmt.Printf("Oper Status: %s\n", operFmt)

		fmt.Printf("Speed: %s\n", detail.Speed)
		fmt.Printf("MTU: %d\n", detail.MTU)

		if len(detail.IPAddresses) > 0 {
			fmt.Println("\nIP Addresses:")
			for _, ip := range detail.IPAddresses {
				fmt.Printf("  %s\n", ip)
			}
		}

		if detail.VRF != "" {
			fmt.Printf("\nVRF: %s\n", detail.VRF)
		}

		if detail.Service != "" {
			fmt.Printf("\nService: %s\n", detail.Service)
		}

		if detail.PCMember {
			fmt.Printf("\nPortChannel Member of: %s\n", detail.PCParent)
		}

		if detail.IngressACL != "" {
			fmt.Printf("\nIngress ACL: %s\n", detail.IngressACL)
		}
		if detail.EgressACL != "" {
			fmt.Printf("Egress ACL: %s\n", detail.EgressACL)
		}

		return nil
	},
}

var interfaceGetCmd = &cobra.Command{
	Use:   "get <interface> <property>",
	Short: "Get a specific property value",
	Long: `Get a specific property value from an interface.

Requires -D (device) flag.

Properties:
  mtu           - Interface MTU
  admin-status  - Administrative status (up/down)
  oper-status   - Operational status
  speed         - Interface speed
  description   - Interface description
  vrf           - VRF binding
  ip            - IP addresses

Examples:
  newtron -D leaf1-ny interface get Ethernet0 mtu
  newtron -D leaf1-ny interface get Ethernet0 admin-status
  newtron -D leaf1-ny interface get Ethernet0 vrf`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		property := args[1]

		if err := requireDevice(); err != nil {
			return err
		}

		detail, err := app.client.ShowInterface(app.deviceName, intfName)
		if err != nil {
			return err
		}

		var value interface{}
		switch property {
		case "mtu":
			value = detail.MTU
		case "admin-status":
			value = detail.AdminStatus
		case "oper-status":
			value = detail.OperStatus
		case "speed":
			value = detail.Speed
		case "description":
			value = "(not available)"
		case "vrf":
			value = detail.VRF
		case "ip":
			value = strings.Join(detail.IPAddresses, ", ")
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

Requires -D (device) flag.

Properties:
  mtu <value>           - Interface MTU
  admin-status <up|down> - Administrative status
  description <text>    - Interface description
  vrf <name>            - VRF binding
  ip <address/prefix>   - IP address

Examples:
  newtron -D leaf1-ny interface set Ethernet0 mtu 9000 -x
  newtron -D leaf1-ny interface set Ethernet0 admin-status down -x
  newtron -D leaf1-ny interface set Ethernet0 description "Uplink to spine" -x`,
	Args: cobra.MinimumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		property := args[1]
		value := strings.Join(args[2:], " ")
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.InterfaceSet(app.deviceName, intfName, property, value, execOpts()))
	},
}

var interfaceListAclsCmd = &cobra.Command{
	Use:   "list-acls <interface>",
	Short: "List ACLs bound to an interface",
	Long: `List ACLs bound to an interface.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny interface list-acls Ethernet0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		if err := requireDevice(); err != nil {
			return err
		}

		detail, err := app.client.ShowInterface(app.deviceName, intfName)
		if err != nil {
			return err
		}

		ingressACL := detail.IngressACL
		egressACL := detail.EgressACL

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

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny interface list-members PortChannel100
  newtron -D leaf1-ny interface list-members Vlan100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		if err := requireDevice(); err != nil {
			return err
		}

		detail, err := app.client.ShowInterface(app.deviceName, intfName)
		if err != nil {
			return err
		}

		// Check if it's a PortChannel
		if len(detail.PCMembers) > 0 {
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(detail.PCMembers)
			}
			fmt.Println("LAG Members:")
			for _, m := range detail.PCMembers {
				fmt.Printf("  %s\n", m)
			}
			return nil
		}

		// Check if it's a VLAN
		if len(detail.VLANMembers) > 0 {
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(detail.VLANMembers)
			}
			fmt.Println("VLAN Members:")
			for _, m := range detail.VLANMembers {
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
