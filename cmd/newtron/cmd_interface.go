package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/cli"
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

		// The platform-supported inventory: every interface the node's platform
		// declares, with its NIC slot, topology wiring, and authored port config.
		// Live admin/oper state is per-interface (interface show <name>).
		t := cli.NewTable("INTERFACE", "NIC", "WIRED", "PEER", "CONFIGURED")

		for _, intf := range interfaces {
			wired := "no"
			if intf.Used {
				wired = "yes"
			}
			configured := "-"
			if intf.Config != nil {
				configured = "yes"
			}
			t.Row(intf.Name, fmt.Sprintf("%d", intf.NICIndex), wired, dash(intf.Peer), configured)
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
		return displayWriteResult(app.client.SetProperty(app.deviceName, intfName, property, value, execOpts()))
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

var interfaceClearCmd = &cobra.Command{
	Use:   "clear <interface> <property>",
	Short: "Clear a property from an interface",
	Long: `Clear (remove) a property from an interface.

This is the reverse of 'interface set'. It removes the specified property,
restoring the default or removing the configuration entirely.

Requires -D (device) flag.

Examples:
  newtron leaf1 interface clear Ethernet0 vrf -x
  newtron leaf1 interface clear Ethernet0 ip -x
  newtron leaf1 interface clear Ethernet0 description -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		property := args[1]
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.ClearProperty(app.deviceName, intfName, property, execOpts()))
	},
}

var interfaceBindingCmd = &cobra.Command{
	Use:   "binding <interface>",
	Short: "Show the service binding on an interface",
	Long: `Show the service binding details for an interface.

Returns the service name, IP addresses, and VRF associated with the
interface's current service binding. Returns empty if no service is bound.

Requires -D (device) flag.

Examples:
  newtron leaf1 interface binding Ethernet0
  newtron leaf1 interface binding Ethernet0 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		if err := requireDevice(); err != nil {
			return err
		}

		binding, err := app.client.ShowServiceBinding(app.deviceName, intfName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(binding)
		}

		if binding.Service == "" {
			fmt.Println("(no service bound)")
			return nil
		}

		fmt.Printf("Service: %s\n", bold(binding.Service))
		if len(binding.IPAddresses) > 0 {
			fmt.Printf("IP Addresses: %s\n", strings.Join(binding.IPAddresses, ", "))
		}
		if binding.VRF != "" {
			fmt.Printf("VRF: %s\n", binding.VRF)
		}

		return nil
	},
}

var interfaceRemoveTrunkVlanCmd = &cobra.Command{
	Use:   "remove-trunk-vlan <interface> <vlan-id>",
	Short: "Remove one VLAN from an interface's trunk membership",
	Long: `Atomically strip a single tagged VLAN from a trunk port.

Other VLANs on the trunk, the access VLAN (if any), VRF/IP bindings,
BGP peers, QoS, and ACL bindings are untouched. Use this instead of
unconfigure-interface (which tears down the entire port) when you only
want to remove one VLAN. Issue #224.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 interface remove-trunk-vlan Ethernet0 20 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		vlanID, err := strconv.Atoi(args[1])
		if err != nil || vlanID <= 0 {
			return fmt.Errorf("vlan-id must be a positive integer")
		}
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.RemoveTrunkVLAN(app.deviceName, intfName, vlanID, execOpts()))
	},
}

func init() {
	interfaceCmd.AddCommand(interfaceListCmd)
	interfaceCmd.AddCommand(interfaceShowCmd)
	interfaceCmd.AddCommand(interfaceStatusCmd)
	interfaceCmd.AddCommand(interfaceGetCmd)
	interfaceCmd.AddCommand(interfaceSetCmd)
	interfaceCmd.AddCommand(interfaceClearCmd)
	interfaceCmd.AddCommand(interfaceBindingCmd)
	interfaceCmd.AddCommand(interfaceListAclsCmd)
	interfaceCmd.AddCommand(interfaceListMembersCmd)
	interfaceCmd.AddCommand(interfaceRemoveTrunkVlanCmd)
}

var interfaceStatusCmd = &cobra.Command{
	Use:   "status <interface>",
	Short: "Show live operational status (counters, rates, ARP, LLDP, optics)",
	Long: `Show the interface's composed live operational picture, read across the
device's STATE_DB, APPL_DB, and COUNTERS_DB in one call: link state, cumulative
counters, SONiC-computed rates, resolved ARP neighbors, the LLDP far end, and
transceiver data (physical hardware only).

Neighbors shown are RESOLVED entries — an expected-but-missing neighbor means
ARP never resolved on this interface.

Requires -D (device) flag and a live device.

Examples:
  newtron -D leaf1 interface status Ethernet0
  newtron -D leaf1 interface status Ethernet4 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		st, err := app.client.InterfaceStatus(app.deviceName, args[0])
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(st)
		}

		fmt.Printf("Interface: %s\n", bold(st.Name))
		fmt.Printf("Admin Status: %s\n", formatAdminStatus(st.AdminStatus))
		fmt.Printf("Oper Status: %s\n", formatOperStatus(st.OperStatus))
		fmt.Printf("Speed: %s\n", st.Speed)
		fmt.Printf("MTU: %s\n", st.MTU)
		if st.FEC != "" {
			fmt.Printf("FEC: %s\n", st.FEC)
		}
		if st.HostTxReady != "" {
			fmt.Printf("Host TX Ready: %s\n", st.HostTxReady)
		}

		if st.Counters != nil {
			c := st.Counters
			fmt.Println()
			t := cli.NewTable("", "OCTETS", "UCAST", "NON-UCAST", "DISCARDS", "ERRORS")
			t.Row("RX", fmt.Sprintf("%d", c.RxOctets), fmt.Sprintf("%d", c.RxUnicastPackets),
				fmt.Sprintf("%d", c.RxNonUnicastPkts), fmt.Sprintf("%d", c.RxDiscards), fmt.Sprintf("%d", c.RxErrors))
			t.Row("TX", fmt.Sprintf("%d", c.TxOctets), fmt.Sprintf("%d", c.TxUnicastPackets),
				fmt.Sprintf("%d", c.TxNonUnicastPkts), fmt.Sprintf("%d", c.TxDiscards), fmt.Sprintf("%d", c.TxErrors))
			t.Flush()
		}

		if st.Rates != nil {
			r := st.Rates
			fmt.Println()
			t := cli.NewTable("", "BPS", "PPS")
			t.Row("RX", fmt.Sprintf("%.1f", r.RxBps), fmt.Sprintf("%.1f", r.RxPps))
			t.Row("TX", fmt.Sprintf("%.1f", r.TxBps), fmt.Sprintf("%.1f", r.TxPps))
			t.Flush()
			if r.FecPreBer != 0 || r.FecPostBer != 0 {
				fmt.Printf("FEC BER (pre/post): %g / %g\n", r.FecPreBer, r.FecPostBer)
			}
		}

		fmt.Println()
		if len(st.Neighbors) > 0 {
			t := cli.NewTable("NEIGHBOR", "MAC", "FAMILY")
			for _, n := range st.Neighbors {
				t.Row(n.Address, n.MAC, n.Family)
			}
			t.Flush()
		} else {
			fmt.Println("Neighbors: none resolved")
		}

		if st.LLDPPeer != nil {
			fmt.Printf("LLDP Peer: %s port %s", st.LLDPPeer.SystemName, st.LLDPPeer.PortID)
			if st.LLDPPeer.PortDescription != "" {
				fmt.Printf(" (%s)", st.LLDPPeer.PortDescription)
			}
			fmt.Println()
		}

		if st.Optics != nil && st.Optics.Present {
			fmt.Println("\nOptics:")
			printFieldTable(st.Optics.Info)
			if len(st.Optics.DOM) > 0 {
				printFieldTable(st.Optics.DOM)
			}
		}

		return nil
	},
}
