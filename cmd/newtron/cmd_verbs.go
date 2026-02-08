package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/network"
)

// ============================================================================
// READ OPERATIONS (Symmetric with write operations)
// ============================================================================

// showCmd displays object details based on context flags
var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show object details",
	Long: `Show details of the selected object.

With -d only: shows device details
With -d and -i: shows interface details

Examples:
  newtron -d leaf1-ny show                 # Device details
  newtron -d leaf1-ny -i Ethernet0 show    # Interface details
  newtron -d leaf1-ny -i Vlan100 show      # VLAN details
  newtron -d leaf1-ny -i PortChannel100 show  # LAG details`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		if deviceName == "" {
			return fmt.Errorf("device required: use -d <device> flag")
		}

		dev, err := net.ConnectDevice(ctx, deviceName)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		// If interface specified, show interface details
		if interfaceName != "" {
			return showInterface(dev)
		}

		// Otherwise show device details
		return showDevice(dev)
	},
}

func showDevice(dev *network.Device) error {
	fmt.Printf("Device: %s\n", bold(dev.Name()))
	fmt.Printf("Management IP: %s\n", dev.Profile().MgmtIP)
	fmt.Printf("Loopback IP: %s\n", dev.Profile().LoopbackIP)
	fmt.Printf("Platform: %s\n", dev.Profile().Platform)
	fmt.Printf("Site: %s\n", dev.Profile().Site)

	fmt.Println("\nDerived Configuration:")
	fmt.Printf("  BGP Local AS: %d\n", dev.ASNumber())
	fmt.Printf("  BGP Router ID: %s\n", dev.RouterID())
	fmt.Printf("  VTEP Source: %s via Loopback0\n", dev.VTEPSourceIP())

	if neighbors := dev.BGPNeighbors(); len(neighbors) > 0 {
		fmt.Printf("  BGP EVPN Neighbors: %v\n", neighbors)
	}

	fmt.Println("\nState:")
	fmt.Printf("  Interfaces: %d\n", len(dev.ListInterfaces()))
	fmt.Printf("  PortChannels: %d\n", len(dev.ListPortChannels()))
	fmt.Printf("  VLANs: %d\n", len(dev.ListVLANs()))
	fmt.Printf("  VRFs: %d\n", len(dev.ListVRFs()))

	return nil
}

func showInterface(dev *network.Device) error {
	intf, err := dev.GetInterface(interfaceName)
	if err != nil {
		return err
	}

	fmt.Printf("Interface: %s\n", bold(interfaceName))

	// Status
	adminStatus := intf.AdminStatus()
	operStatus := intf.OperStatus()
	if adminStatus == "up" {
		fmt.Printf("Admin Status: %s\n", green("up"))
	} else {
		fmt.Printf("Admin Status: %s\n", red(adminStatus))
	}
	if operStatus == "up" {
		fmt.Printf("Oper Status: %s\n", green("up"))
	} else {
		fmt.Printf("Oper Status: %s\n", red(operStatus))
	}

	// Properties
	fmt.Printf("Speed: %s\n", intf.Speed())
	fmt.Printf("MTU: %d\n", intf.MTU())
	if desc := intf.Description(); desc != "" {
		fmt.Printf("Description: %s\n", desc)
	}

	// IP addresses
	if addrs := intf.IPAddresses(); len(addrs) > 0 {
		fmt.Println("\nIP Addresses:")
		for _, ip := range addrs {
			fmt.Printf("  %s\n", ip)
		}
	}

	// VRF
	if vrf := intf.VRF(); vrf != "" {
		fmt.Printf("\nVRF: %s\n", vrf)
	}

	// Service
	if svc := intf.ServiceName(); svc != "" {
		fmt.Printf("\nService: %s\n", svc)
	}

	// LAG membership
	if intf.IsLAGMember() {
		fmt.Printf("\nLAG Member of: %s\n", intf.LAGParent())
	}

	// LAG members (if this is a PortChannel)
	if members := intf.LAGMembers(); len(members) > 0 {
		fmt.Printf("\nLAG Members: %s\n", strings.Join(members, ", "))
	}

	// VLAN members (if this is a VLAN)
	if members := intf.VLANMembers(); len(members) > 0 {
		fmt.Printf("\nVLAN Members: %s\n", strings.Join(members, ", "))
	}

	// MAC-VPN (if this is a VLAN with EVPN binding)
	if macvpnInfo := intf.MACVPNInfo(); macvpnInfo != nil && macvpnInfo.L2VNI > 0 {
		fmt.Println("\nMAC-VPN:")
		if macvpnInfo.Name != "" {
			fmt.Printf("  Name: %s\n", macvpnInfo.Name)
		}
		fmt.Printf("  L2VNI: %d\n", macvpnInfo.L2VNI)
		fmt.Printf("  ARP Suppression: %v\n", macvpnInfo.ARPSuppression)
	}

	// ACLs
	if acl := intf.IngressACL(); acl != "" {
		fmt.Printf("\nIngress ACL: %s\n", acl)
	}
	if acl := intf.EgressACL(); acl != "" {
		fmt.Printf("Egress ACL: %s\n", acl)
	}

	return nil
}

// getCmd gets a specific property value
var getCmd = &cobra.Command{
	Use:   "get <property>",
	Short: "Get a specific property value",
	Long: `Get a specific property value from the selected interface.

Requires -d (device) and -i (interface) flags.

Properties:
  mtu           - Interface MTU
  admin-status  - Administrative status (up/down)
  oper-status   - Operational status
  speed         - Interface speed
  description   - Interface description
  vrf           - VRF binding
  ip            - IP addresses

Examples:
  newtron -d leaf1-ny -i Ethernet0 get mtu
  newtron -d leaf1-ny -i Ethernet0 get admin-status
  newtron -d leaf1-ny -i Ethernet0 get vrf`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		property := args[0]
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

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

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{property: value})
		}
		fmt.Println(value)
		return nil
	},
}

// getServiceCmd gets the service bound to an interface
var getServiceCmd = &cobra.Command{
	Use:   "get-service",
	Short: "Get the service bound to an interface",
	Long: `Get the service bound to the selected interface.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 get-service`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		svc := intf.ServiceName()
		if svc == "" {
			fmt.Println("(no service bound)")
			return nil
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"service": svc,
				"ip":      strings.Join(intf.IPAddresses(), ", "),
				"vrf":     intf.VRF(),
			})
		}

		fmt.Printf("Service: %s\n", svc)
		if addrs := intf.IPAddresses(); len(addrs) > 0 {
			fmt.Printf("IP: %s\n", strings.Join(addrs, ", "))
		}
		if vrf := intf.VRF(); vrf != "" {
			fmt.Printf("VRF: %s\n", vrf)
		}
		return nil
	},
}

// listMembersCmd lists members of a LAG or VLAN
var listMembersCmd = &cobra.Command{
	Use:   "list-members",
	Short: "List members of a LAG or VLAN",
	Long: `List members of the selected LAG (PortChannel) or VLAN.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i PortChannel100 list-members
  newtron -d leaf1-ny -i Vlan100 list-members`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		// Check if it's a LAG
		if members := intf.LAGMembers(); len(members) > 0 {
			if jsonOutput {
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
			if jsonOutput {
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

// listAclsCmd lists ACLs bound to an interface
var listAclsCmd = &cobra.Command{
	Use:   "list-acls",
	Short: "List ACLs bound to an interface",
	Long: `List ACLs bound to the selected interface.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 list-acls`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		ingressACL := intf.IngressACL()
		egressACL := intf.EgressACL()

		if jsonOutput {
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

// getMacvpnCmd gets the MAC-VPN binding for a VLAN
var getMacvpnCmd = &cobra.Command{
	Use:   "get-macvpn",
	Short: "Get MAC-VPN binding for a VLAN",
	Long: `Get the MAC-VPN binding for the selected VLAN.

Shows the macvpn name, L2VNI, and ARP suppression settings.
Requires -d (device) and -i (VLAN interface) flags.

Examples:
  newtron -d leaf1-ny -i Vlan100 get-macvpn`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		macvpnInfo := intf.MACVPNInfo()

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(macvpnInfo)
		}

		if macvpnInfo == nil || macvpnInfo.L2VNI == 0 {
			fmt.Println("(no MAC-VPN bound)")
			return nil
		}

		fmt.Printf("MAC-VPN: %s\n", macvpnInfo.Name)
		fmt.Printf("  L2VNI: %d\n", macvpnInfo.L2VNI)
		fmt.Printf("  ARP Suppression: %v\n", macvpnInfo.ARPSuppression)
		return nil
	},
}

// listBgpCmd lists BGP neighbors on an interface
var listBgpCmd = &cobra.Command{
	Use:   "list-bgp-neighbors",
	Short: "List BGP neighbors",
	Long: `List BGP neighbors on the selected interface or device.

With -d only: lists all BGP neighbors on the device
With -d and -i: lists BGP neighbors on the specific interface

Examples:
  newtron -d leaf1-ny list-bgp-neighbors
  newtron -d leaf1-ny -i Ethernet0 list-bgp-neighbors`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		var neighbors []string
		if interfaceName != "" {
			intf, err := dev.GetInterface(interfaceName)
			if err != nil {
				return err
			}
			neighbors = intf.BGPNeighbors()
		} else {
			neighbors = dev.ListBGPNeighbors()
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(neighbors)
		}

		if len(neighbors) == 0 {
			fmt.Println("(no BGP neighbors)")
			return nil
		}

		fmt.Println("BGP Neighbors:")
		for _, n := range neighbors {
			fmt.Printf("  %s\n", n)
		}
		return nil
	},
}

// listCmd lists child objects
var listCmd = &cobra.Command{
	Use:   "list <type>",
	Short: "List child objects",
	Long: `List child objects of the specified type.

Without -d: lists network-level objects (services, devices)
With -d: lists device-level objects (interfaces, vlans, lags, vrfs, acls)

Examples:
  newtron list services                # Network-level
  newtron list devices                 # Network-level
  newtron -d leaf1-ny list interfaces  # Device-level
  newtron -d leaf1-ny list vlans       # Device-level
  newtron -d leaf1-ny list lags        # Device-level`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		objType := args[0]
		ctx := context.Background()

		// Network-level lists (no device required)
		if deviceName == "" {
			switch objType {
			case "services":
				services := net.ListServices()
				if jsonOutput {
					return json.NewEncoder(os.Stdout).Encode(services)
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "NAME\tTYPE\tDESCRIPTION")
				fmt.Fprintln(w, "----\t----\t-----------")
				for _, name := range services {
					svc, _ := net.GetService(name)
					fmt.Fprintf(w, "%s\t%s\t%s\n", name, svc.ServiceType, svc.Description)
				}
				w.Flush()
				return nil

			case "devices":
				devices := net.ListDevices()
				if jsonOutput {
					return json.NewEncoder(os.Stdout).Encode(devices)
				}
				for _, d := range devices {
					fmt.Println(d)
				}
				return nil

			default:
				return fmt.Errorf("use -d <device> to list %s", objType)
			}
		}

		// Device-level lists
		dev, err := net.ConnectDevice(ctx, deviceName)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		switch objType {
		case "interfaces":
			interfaces := dev.ListInterfaces()
			if jsonOutput {
				// Build detailed JSON output
				type intfInfo struct {
					Name    string `json:"name"`
					Admin   string `json:"admin_status"`
					Oper    string `json:"oper_status"`
					Service string `json:"service,omitempty"`
					IP      string `json:"ip,omitempty"`
					VRF     string `json:"vrf,omitempty"`
					IPVPN   string `json:"ipvpn,omitempty"`
					MACVPN  string `json:"macvpn,omitempty"`
				}
				var infos []intfInfo
				for _, name := range interfaces {
					intf, _ := dev.GetInterface(name)
					info := intfInfo{
						Name:    name,
						Admin:   intf.AdminStatus(),
						Oper:    intf.OperStatus(),
						Service: intf.ServiceName(),
						IP:      intf.ServiceIP(),
						VRF:     intf.ServiceVRF(),
						IPVPN:   intf.ServiceIPVPN(),
						MACVPN:  intf.ServiceMACVPN(),
					}
					infos = append(infos, info)
				}
				return json.NewEncoder(os.Stdout).Encode(infos)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "INTERFACE\tADMIN\tOPER\tSERVICE\tIP\tVRF\tIPVPN\tMACVPN")
			fmt.Fprintln(w, "---------\t-----\t----\t-------\t--\t---\t-----\t------")
			for _, name := range interfaces {
				intf, _ := dev.GetInterface(name)
				svc := intf.ServiceName()
				if svc == "" {
					svc = "-"
				}
				ip := intf.ServiceIP()
				if ip == "" {
					ip = "-"
				}
				vrf := intf.ServiceVRF()
				if vrf == "" {
					vrf = "-"
				}
				ipvpn := intf.ServiceIPVPN()
				if ipvpn == "" {
					ipvpn = "-"
				}
				macvpn := intf.ServiceMACVPN()
				if macvpn == "" {
					macvpn = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					name, intf.AdminStatus(), intf.OperStatus(), svc, ip, vrf, ipvpn, macvpn)
			}
			w.Flush()

		case "vlans":
			vlans := dev.ListVLANs()
			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(vlans)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "VLAN\tNAME\tMAC-VPN\tL2VNI\tARP-SUPP")
			fmt.Fprintln(w, "----\t----\t-------\t-----\t--------")
			for _, vlanID := range vlans {
				vlan, _ := dev.GetVLAN(vlanID)
				macvpnName := "-"
				l2vni := "-"
				arpSupp := "-"
				if vlan.MACVPNInfo != nil {
					if vlan.MACVPNInfo.Name != "" {
						macvpnName = vlan.MACVPNInfo.Name
					}
					if vlan.MACVPNInfo.L2VNI > 0 {
						l2vni = fmt.Sprintf("%d", vlan.MACVPNInfo.L2VNI)
					}
					if vlan.MACVPNInfo.ARPSuppression {
						arpSupp = "on"
					} else {
						arpSupp = "off"
					}
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", vlanID, vlan.Name, macvpnName, l2vni, arpSupp)
			}
			w.Flush()

		case "lags", "portchannels":
			pcs := dev.ListPortChannels()
			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(pcs)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS\tMEMBERS")
			fmt.Fprintln(w, "----\t------\t-------")
			for _, name := range pcs {
				pc, _ := dev.GetPortChannel(name)
				fmt.Fprintf(w, "%s\t%s\t%s\n",
					name, pc.AdminStatus, strings.Join(pc.Members, ","))
			}
			w.Flush()

		case "vrfs":
			vrfs := dev.ListVRFs()
			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(vrfs)
			}
			for _, vrf := range vrfs {
				fmt.Println(vrf)
			}

		case "acls":
			acls := dev.ListACLTables()
			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(acls)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tTYPE\tSTAGE\tPORTS")
			fmt.Fprintln(w, "----\t----\t-----\t-----")
			for _, name := range acls {
				acl, _ := dev.GetACLTable(name)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					name, acl.Type, acl.Stage, acl.Ports)
			}
			w.Flush()

		default:
			return fmt.Errorf("unknown type: %s (try: interfaces, vlans, lags, vrfs, acls)", objType)
		}

		return nil
	},
}

// ============================================================================
// WRITE OPERATIONS
// ============================================================================

// setCmd sets a property on the selected interface
var setCmd = &cobra.Command{
	Use:   "set <property> <value>",
	Short: "Set a property on the selected interface",
	Long: `Set a property on the selected interface.

Requires -d (device) and -i (interface) flags.

Properties:
  mtu <value>           - Interface MTU
  admin-status <up|down> - Administrative status
  description <text>    - Interface description
  vrf <name>            - VRF binding
  ip <address/prefix>   - IP address

Examples:
  newtron -d leaf1-ny -i Ethernet0 set mtu 9000 -x
  newtron -d leaf1-ny -i Ethernet0 set admin-status down -x
  newtron -d leaf1-ny -i Ethernet0 set description "Uplink to spine" -x`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		property := args[0]
		value := strings.Join(args[1:], " ")
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		// Check permissions
		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermInterfaceModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.Set(ctx, property, value)
		if err != nil {
			return fmt.Errorf("setting %s: %w", property, err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// createCmd creates a new object at device level
var createCmd = &cobra.Command{
	Use:   "create <type> <name>",
	Short: "Create a new object",
	Long: `Create a new object on the device.

Requires -d (device) flag.

Types:
  vlan <id>      - Create VLAN
  lag <name>     - Create PortChannel
  vrf <name>     - Create VRF
  vtep           - Create VTEP
  acl <name>     - Create ACL table

Examples:
  newtron -d leaf1-ny create vlan 100 --name "Servers" -x
  newtron -d leaf1-ny create lag PortChannel100 --members Ethernet0,Ethernet4 -x
  newtron -d leaf1-ny create vrf Vrf_CUST1 --l3vni 10001 -x
  newtron -d leaf1-ny create vtep --source-ip 10.0.0.10 -x`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		objType := args[0]
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		var changeSet *network.ChangeSet

		switch objType {
		case "vlan":
			if len(args) < 2 {
				return fmt.Errorf("vlan ID required")
			}
			var vlanID int
			fmt.Sscanf(args[1], "%d", &vlanID)
			changeSet, err = dev.CreateVLAN(ctx, vlanID, network.VLANConfig{
				Name: createVlanName,
			})

		case "lag", "portchannel":
			if len(args) < 2 {
				return fmt.Errorf("LAG name required")
			}
			members := []string{}
			if createLagMembers != "" {
				members = strings.Split(createLagMembers, ",")
			}
			changeSet, err = dev.CreatePortChannel(ctx, args[1], network.PortChannelConfig{
				Members:  members,
				MinLinks: createLagMinLinks,
				FastRate: createLagFastRate,
			})

		case "vrf":
			if len(args) < 2 {
				return fmt.Errorf("VRF name required")
			}
			changeSet, err = dev.CreateVRF(ctx, args[1], network.VRFConfig{
				L3VNI: createVrfL3VNI,
			})

		case "vtep":
			sourceIP := createVtepSourceIP
			if sourceIP == "" {
				sourceIP = dev.Profile().LoopbackIP
			}
			changeSet, err = dev.CreateVTEP(ctx, network.VTEPConfig{
				SourceIP: sourceIP,
			})

		case "acl":
			if len(args) < 2 {
				return fmt.Errorf("ACL name required")
			}
			changeSet, err = dev.CreateACLTable(ctx, args[1], network.ACLTableConfig{
				Type:  createAclType,
				Stage: createAclStage,
			})

		default:
			return fmt.Errorf("unknown type: %s", objType)
		}

		if err != nil {
			return fmt.Errorf("creating %s: %w", objType, err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// Flags for create command
var (
	createVlanName     string
	createLagMembers   string
	createLagMinLinks  int
	createLagFastRate  bool
	createVrfL3VNI     int
	createVtepSourceIP string
	createAclType      string
	createAclStage     string
)

// deleteCmd deletes an object at device level
var deleteCmd = &cobra.Command{
	Use:   "delete <type> <name>",
	Short: "Delete an object",
	Long: `Delete an object from the device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny delete vlan 100 -x
  newtron -d leaf1-ny delete lag PortChannel100 -x
  newtron -d leaf1-ny delete vrf Vrf_CUST1 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		objType := args[0]
		objName := args[1]
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		var changeSet *network.ChangeSet

		switch objType {
		case "vlan":
			var vlanID int
			fmt.Sscanf(objName, "%d", &vlanID)
			changeSet, err = dev.DeleteVLAN(ctx, vlanID)

		case "lag", "portchannel":
			changeSet, err = dev.DeletePortChannel(ctx, objName)

		case "vrf":
			changeSet, err = dev.DeleteVRF(ctx, objName)

		case "acl":
			changeSet, err = dev.DeleteACLTable(ctx, objName)

		default:
			return fmt.Errorf("unknown type: %s", objType)
		}

		if err != nil {
			return fmt.Errorf("deleting %s: %w", objType, err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// addMemberCmd adds a member to a LAG or VLAN
var addMemberCmd = &cobra.Command{
	Use:   "add-member <interface>",
	Short: "Add a member to a LAG or VLAN",
	Long: `Add a member interface to the selected LAG or VLAN.

Requires -d (device) and -i (LAG or VLAN) flags.

Examples:
  newtron -d leaf1-ny -i PortChannel100 add-member Ethernet8 -x
  newtron -d leaf1-ny -i Vlan100 add-member Ethernet4 -x
  newtron -d leaf1-ny -i Vlan100 add-member Ethernet4 --tagged -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		memberIntf := args[0]
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.AddMember(ctx, memberIntf, addMemberTagged)
		if err != nil {
			return fmt.Errorf("adding member: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var addMemberTagged bool

// removeMemberCmd removes a member from a LAG or VLAN
var removeMemberCmd = &cobra.Command{
	Use:   "remove-member <interface>",
	Short: "Remove a member from a LAG or VLAN",
	Long: `Remove a member interface from the selected LAG or VLAN.

Requires -d (device) and -i (LAG or VLAN) flags.

Examples:
  newtron -d leaf1-ny -i PortChannel100 remove-member Ethernet8 -x
  newtron -d leaf1-ny -i Vlan100 remove-member Ethernet4 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		memberIntf := args[0]
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.RemoveMember(ctx, memberIntf)
		if err != nil {
			return fmt.Errorf("removing member: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// applyServiceCmd applies a service to an interface
var applyServiceCmd = &cobra.Command{
	Use:   "apply-service <service>",
	Short: "Apply a service to the selected interface",
	Long: `Apply a service to the selected interface.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 apply-service customer-l3 --ip 10.1.1.1/30 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName).WithService(serviceName)
		if err := checkExecutePermission(auth.PermServiceApply, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.ApplyService(ctx, serviceName, network.ApplyServiceOpts{
			IPAddress: applyServiceIP,
			PeerAS:    applyServicePeerAS,
		})
		if err != nil {
			return fmt.Errorf("applying service: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var applyServiceIP string
var applyServicePeerAS int

// removeServiceCmd removes a service from an interface
var removeServiceCmd = &cobra.Command{
	Use:   "remove-service",
	Short: "Remove the service from the selected interface",
	Long: `Remove the service from the selected interface.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 remove-service -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermServiceRemove, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.RemoveService(ctx)
		if err != nil {
			return fmt.Errorf("removing service: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// refreshServiceCmd syncs interface to current service definition
var refreshServiceCmd = &cobra.Command{
	Use:   "refresh-service",
	Short: "Refresh the service on the selected interface",
	Long: `Refresh the service on the selected interface to match the current service definition.

Use this when the service definition (filters, QoS, etc.) has changed in network.json
and you want to sync the interface configuration to match.

This command will:
  1. Compare current interface config with current service definition
  2. Detect changes in filter-specs, QoS profiles, etc.
  3. Create new ACLs from updated filter-specs (if changed)
  4. Unbind old ACLs and bind new ones
  5. Clean up orphaned ACLs (not bound to any interface)

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 refresh-service -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		// Check if interface has a service bound
		if !intf.HasService() {
			return fmt.Errorf("no service bound to interface %s", interfaceName)
		}

		serviceName := intf.ServiceName()
		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName).WithService(serviceName)
		if err := checkExecutePermission(auth.PermServiceApply, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.RefreshService(ctx)
		if err != nil {
			return fmt.Errorf("refreshing service: %w", err)
		}

		if changeSet.IsEmpty() {
			fmt.Println("Service is already in sync with definition. No changes needed.")
			return nil
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		// Show orphaned ACLs that will be cleaned up
		orphans := dev.GetOrphanedACLs()
		if len(orphans) > 0 {
			fmt.Println("\nOrphaned ACLs to be removed:")
			for _, acl := range orphans {
				fmt.Printf("  - %s\n", acl)
			}
		}

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// bindAclCmd binds an ACL to an interface
var bindAclCmd = &cobra.Command{
	Use:   "bind-acl <acl-name>",
	Short: "Bind an ACL to the selected interface",
	Long: `Bind an ACL to the selected interface.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 bind-acl CUSTOM-ACL --direction ingress -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.BindACL(ctx, aclName, bindAclDirection)
		if err != nil {
			return fmt.Errorf("binding ACL: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var bindAclDirection string

// unbindAclCmd unbinds an ACL from an interface
var unbindAclCmd = &cobra.Command{
	Use:   "unbind-acl <acl-name>",
	Short: "Unbind an ACL from the selected interface",
	Long: `Unbind an ACL from the selected interface.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 unbind-acl CUSTOM-ACL -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.UnbindACL(ctx, aclName)
		if err != nil {
			return fmt.Errorf("unbinding ACL: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// bindMacvpnCmd binds a VLAN to a MAC-VPN definition
var bindMacvpnCmd = &cobra.Command{
	Use:   "bind-macvpn <macvpn-name>",
	Short: "Bind the selected VLAN to a MAC-VPN definition",
	Long: `Bind the selected VLAN to a MAC-VPN definition from network.json.

The MAC-VPN definition specifies L2VNI and ARP suppression settings.
Requires -d (device) and -i (VLAN) flags.

Examples:
  newtron -d leaf1-ny -i Vlan100 bind-macvpn servers-vlan100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		macvpnName := args[0]
		ctx := context.Background()

		// Look up macvpn definition
		macvpnDef, err := net.GetMACVPN(macvpnName)
		if err != nil {
			return fmt.Errorf("macvpn '%s' not found in network.json", macvpnName)
		}

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Show what will be applied
		fmt.Printf("MAC-VPN: %s\n", macvpnName)
		fmt.Printf("  L2VNI: %d\n", macvpnDef.L2VNI)
		fmt.Printf("  ARP Suppression: %v\n", macvpnDef.ARPSuppression)
		fmt.Println()

		changeSet, err := intf.BindMACVPN(ctx, macvpnName, macvpnDef)
		if err != nil {
			return fmt.Errorf("binding MAC-VPN: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// unbindMacvpnCmd unbinds the MAC-VPN from a VLAN
var unbindMacvpnCmd = &cobra.Command{
	Use:   "unbind-macvpn",
	Short: "Unbind the MAC-VPN from the selected VLAN",
	Long: `Unbind the MAC-VPN from the selected VLAN.

Removes the L2VNI mapping and ARP suppression settings.
Requires -d (device) and -i (VLAN) flags.

Examples:
  newtron -d leaf1-ny -i Vlan100 unbind-macvpn -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.UnbindMACVPN(ctx)
		if err != nil {
			return fmt.Errorf("unbinding MAC-VPN: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// addBgpCmd adds a BGP neighbor on an interface
var addBgpCmd = &cobra.Command{
	Use:   "add-bgp-neighbor <remote-asn>",
	Short: "Add a BGP neighbor on the selected interface",
	Long: `Add a direct BGP neighbor on the selected interface.

Uses the interface's link IP as the update-source.
Requires -d (device) and -i (interface) flags.

Neighbor IP auto-derivation:
  - For /30 subnets: neighbor IP is automatically computed (other host address)
  - For /31 subnets: neighbor IP is automatically computed (RFC 3021)
  - For /29 or larger: --neighbor-ip is REQUIRED

Security (non-negotiable):
  - TTL is hardcoded to 1 for all direct neighbors (GTSM)
  - Neighbor IP must be on the same subnet as interface

Options:
  --neighbor-ip <ip>  Required for subnets larger than /30
  --passive           Passive mode (wait for peer to connect, mutually exclusive with --neighbor-ip)

Examples:
  # /30 or /31 - neighbor IP auto-derived
  newtron -d leaf1-ny -i Ethernet0 add-bgp-neighbor 65100 -x

  # /29 or larger - must specify neighbor IP
  newtron -d leaf1-ny -i Ethernet0 add-bgp-neighbor 65100 --neighbor-ip 10.1.1.5 -x

  # Passive mode (customer-facing, wait for them to connect)
  newtron -d leaf1-ny -i Ethernet0 add-bgp-neighbor 65100 --passive -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var remoteASN int
		fmt.Sscanf(args[0], "%d", &remoteASN)
		ctx := context.Background()

		// Validate mutually exclusive flags
		if bgpNeighborIP != "" && bgpPassive {
			return fmt.Errorf("--neighbor-ip and --passive are mutually exclusive")
		}

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermBGPModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Build BGP neighbor config
		bgpConfig := network.BGPNeighborConfig{
			RemoteASN: remoteASN,
			Passive:   bgpPassive,
			TTL:       1, // Hardcoded for security - direct neighbors only
		}

		// Determine neighbor IP
		if bgpPassive {
			// Passive mode: no neighbor IP needed (we wait for connection)
			bgpConfig.NeighborIP = ""
		} else if bgpNeighborIP != "" {
			// Explicit neighbor IP provided
			bgpConfig.NeighborIP = bgpNeighborIP
		} else {
			// Try to auto-derive from interface IP
			// This will fail if subnet is larger than /30
			derivedIP, err := intf.DeriveNeighborIP()
			if err != nil {
				return fmt.Errorf("cannot auto-derive neighbor IP: %w\nuse --neighbor-ip for subnets larger than /30", err)
			}
			bgpConfig.NeighborIP = derivedIP
		}

		changeSet, err := intf.AddBGPNeighborWithConfig(ctx, bgpConfig)
		if err != nil {
			return fmt.Errorf("adding BGP neighbor: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		// Show derived values
		if bgpConfig.NeighborIP != "" && bgpNeighborIP == "" {
			fmt.Printf("\nDerived neighbor IP: %s\n", bgpConfig.NeighborIP)
		}
		fmt.Println("TTL: 1 (hardcoded for direct neighbors)")

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

// BGP neighbor flags
var (
	bgpNeighborIP string
	bgpPassive    bool
)

// removeBgpCmd removes a BGP neighbor from an interface
var removeBgpCmd = &cobra.Command{
	Use:   "remove-bgp-neighbor",
	Short: "Remove a BGP neighbor from the selected interface",
	Long: `Remove a BGP neighbor from the selected interface.

Requires -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 remove-bgp-neighbor -x
  newtron -d leaf1-ny -i Ethernet0 remove-bgp-neighbor --neighbor-ip 10.1.1.2 -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(interfaceName)
		if err := checkExecutePermission(auth.PermBGPModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := intf.RemoveBGPNeighbor(ctx, removeBgpNeighborIP)
		if err != nil {
			return fmt.Errorf("removing BGP neighbor: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var removeBgpNeighborIP string

// healthCheckVerbCmd runs health checks on a device (top-level verb)
var healthCheckVerbCmd = &cobra.Command{
	Use:   "health-check",
	Short: "Run health checks on the device",
	Long: `Run health checks on the selected device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny health-check
  newtron -d leaf1-ny health-check --check bgp`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		results, err := dev.RunHealthChecks(ctx, healthCheckVerbType)
		if err != nil {
			return fmt.Errorf("running health checks: %w", err)
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(results)
		}

		fmt.Printf("Health Check Results for %s\n", bold(deviceName))
		fmt.Println(strings.Repeat("=", 40))

		for _, r := range results {
			status := green("[PASS]")
			if r.Status == "warn" {
				status = yellow("[WARN]")
			} else if r.Status == "fail" {
				status = red("[FAIL]")
			}
			fmt.Printf("%s %s: %s\n", status, r.Check, r.Message)
		}

		return nil
	},
}

var healthCheckVerbType string

// applyBaselineCmd applies a baseline configlet to a device
var applyBaselineCmd = &cobra.Command{
	Use:   "apply-baseline <configlet>",
	Short: "Apply a baseline configlet to the device",
	Long: `Apply a baseline configlet to the selected device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny apply-baseline sonic-baseline -x
  newtron -d leaf1-ny apply-baseline sonic-evpn -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configletName := args[0]
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(configletName)
		if err := checkExecutePermission(auth.PermBaselineApply, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.ApplyBaseline(ctx, configletName, baselineVars)
		if err != nil {
			return fmt.Errorf("applying baseline: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var baselineVars []string

// cleanupCmd removes orphaned configurations from a device
var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove orphaned configurations from the device",
	Long: `Remove orphaned configurations from the device.

This command identifies and removes configurations that are no longer in use:
  - ACL tables not bound to any interface
  - VRFs with no interface bindings
  - VNI mappings for deleted VLANs/VRFs
  - Unused EVPN route targets

Philosophy: Only active configurations should exist on the device.
This prevents unbounded growth of stale/orphaned config settings.

Requires -d (device) flag.

Examples:
  # Preview what would be cleaned up
  newtron -d leaf1-ny cleanup

  # Execute cleanup
  newtron -d leaf1-ny cleanup -x

  # Cleanup specific type only
  newtron -d leaf1-ny cleanup --type acls -x
  newtron -d leaf1-ny cleanup --type vrfs -x`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Collect orphaned resources
		changeSet, summary, err := dev.Cleanup(ctx, cleanupType)
		if err != nil {
			return fmt.Errorf("analyzing orphaned configs: %w", err)
		}

		if changeSet.IsEmpty() {
			fmt.Println("No orphaned configurations found. Device is clean.")
			return nil
		}

		// Display summary
		fmt.Printf("Orphaned Configurations on %s\n", bold(deviceName))
		fmt.Println(strings.Repeat("=", 50))

		if len(summary.OrphanedACLs) > 0 {
			fmt.Printf("\nOrphaned ACLs (%d):\n", len(summary.OrphanedACLs))
			for _, acl := range summary.OrphanedACLs {
				fmt.Printf("  - %s\n", acl)
			}
		}

		if len(summary.OrphanedVRFs) > 0 {
			fmt.Printf("\nOrphaned VRFs (%d):\n", len(summary.OrphanedVRFs))
			for _, vrf := range summary.OrphanedVRFs {
				fmt.Printf("  - %s\n", vrf)
			}
		}

		if len(summary.OrphanedVNIMappings) > 0 {
			fmt.Printf("\nOrphaned VNI Mappings (%d):\n", len(summary.OrphanedVNIMappings))
			for _, vni := range summary.OrphanedVNIMappings {
				fmt.Printf("  - %s\n", vni)
			}
		}

		fmt.Println("\nChanges to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var cleanupType string

// configureSVICmd configures a VLAN's SVI (Layer 3 interface)
var configureSVICmd = &cobra.Command{
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
  newtron -d leaf1-ny configure-svi 100 --vrf Vrf_CUST1 --ip 10.1.100.1/24 -x
  newtron -d leaf1-ny configure-svi 100 --ip 10.1.100.1/24 --anycast-gw 00:00:00:00:01:01 -x
  newtron -d leaf1-ny configure-svi 200 --vrf Vrf_CUST1 --ip 10.1.200.1/24 --anycast-gw 00:00:00:00:01:01 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var vlanID int
		if _, err := fmt.Sscanf(args[0], "%d", &vlanID); err != nil {
			return fmt.Errorf("invalid VLAN ID: %s", args[0])
		}
		ctx := context.Background()

		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
		if err := checkExecutePermission(auth.PermInterfaceModify, authCtx); err != nil {
			return err
		}

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.ConfigureSVI(ctx, vlanID, network.SVIConfig{
			VRF:        sviVRF,
			IPAddress:  sviIP,
			AnycastMAC: sviAnycastGW,
		})
		if err != nil {
			return fmt.Errorf("configuring SVI: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var (
	sviVRF       string
	sviIP        string
	sviAnycastGW string
)

func init() {
	// Flags for create command
	createCmd.Flags().StringVar(&createVlanName, "name", "", "VLAN name")
	createCmd.Flags().StringVar(&createLagMembers, "members", "", "Comma-separated list of LAG members")
	createCmd.Flags().IntVar(&createLagMinLinks, "min-links", 1, "Minimum links for LAG")
	createCmd.Flags().BoolVar(&createLagFastRate, "fast-rate", true, "Use LACP fast rate")
	createCmd.Flags().IntVar(&createVrfL3VNI, "l3vni", 0, "L3VNI for VRF")
	createCmd.Flags().StringVar(&createVtepSourceIP, "source-ip", "", "VTEP source IP (default: loopback)")
	createCmd.Flags().StringVar(&createAclType, "type", "L3", "ACL type (L3, L3V6)")
	createCmd.Flags().StringVar(&createAclStage, "stage", "ingress", "ACL stage (ingress, egress)")

	// Flags for add-member
	addMemberCmd.Flags().BoolVar(&addMemberTagged, "tagged", false, "Add as tagged VLAN member")

	// Flags for apply-service
	applyServiceCmd.Flags().StringVar(&applyServiceIP, "ip", "", "IP address (required)")
	applyServiceCmd.Flags().IntVar(&applyServicePeerAS, "peer-as", 0, "BGP peer AS number")

	// Flags for bind-acl
	bindAclCmd.Flags().StringVar(&bindAclDirection, "direction", "ingress", "ACL direction (ingress, egress)")

	// Flags for add-bgp-neighbor
	addBgpCmd.Flags().StringVar(&bgpNeighborIP, "neighbor-ip", "", "Neighbor IP (required for subnets larger than /30)")
	addBgpCmd.Flags().BoolVar(&bgpPassive, "passive", false, "Passive mode (wait for peer to connect)")

	// Flags for remove-bgp-neighbor
	removeBgpCmd.Flags().StringVar(&removeBgpNeighborIP, "neighbor-ip", "", "Specific neighbor IP to remove")

	// Flags for health-check
	healthCheckVerbCmd.Flags().StringVar(&healthCheckVerbType, "check", "", "Specific check to run (bgp, interfaces, evpn)")

	// Flags for apply-baseline
	applyBaselineCmd.Flags().StringArrayVar(&baselineVars, "var", nil, "Variable in key=value format")

	// Flags for cleanup
	cleanupCmd.Flags().StringVar(&cleanupType, "type", "", "Cleanup specific type only (acls, vrfs, vnis)")

	// Flags for configure-svi
	configureSVICmd.Flags().StringVar(&sviVRF, "vrf", "", "VRF to bind the SVI to")
	configureSVICmd.Flags().StringVar(&sviIP, "ip", "", "IP address with prefix (e.g., 10.1.100.1/24)")
	configureSVICmd.Flags().StringVar(&sviAnycastGW, "anycast-gw", "", "Anycast gateway MAC (SAG)")
}
