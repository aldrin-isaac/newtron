package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/network"
)

var interactiveCmd = &cobra.Command{
	Use:   "interactive",
	Short: "Enter interactive mode",
	Long: `Enter interactive menu mode for device configuration.

Use -d (device) flag to connect to a device.

In interactive mode, you can navigate through menus to:
  - Apply and remove services
  - Configure interfaces and LAGs
  - Manage VLANs
  - View health status

Examples:
  newtron interactive
  newtron -d leaf1-ny interactive`,
	Aliases: []string{"i"},
	RunE: func(cmd *cobra.Command, args []string) error {
		var dev *network.Device

		if deviceName != "" {
			ctx := context.Background()

			fmt.Printf("Connecting to %s...\n", deviceName)
			var err error
			dev, err = net.ConnectDevice(ctx, deviceName)
			if err != nil {
				return fmt.Errorf("connecting: %w", err)
			}
			defer dev.Disconnect()

			fmt.Println(green("Connected."))
		}

		runInteractiveMode(dev, deviceName)
		return nil
	},
}

func runInteractiveMode(dev *network.Device, deviceName string) {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println()
		fmt.Println(bold("=== Newtron Interactive Mode ==="))
		if deviceName != "" {
			fmt.Printf("Device: %s\n", deviceName)
		}
		fmt.Println()
		fmt.Println("Main Menu:")
		fmt.Println("  1. Service Management")
		fmt.Println("  2. Interfaces")
		fmt.Println("  3. Link Aggregation (LAG)")
		fmt.Println("  4. VLANs")
		fmt.Println("  5. ACL/Filters")
		fmt.Println("  6. EVPN")
		fmt.Println("  7. BGP")
		fmt.Println("  8. Health Checks")
		fmt.Println("  9. Baseline")
		fmt.Println("  q. Quit")
		fmt.Println()
		fmt.Print("Select option: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			serviceMenu(reader, dev, deviceName)
		case "2":
			interfaceMenu(reader, dev, deviceName)
		case "3":
			lagMenu(reader, dev, deviceName)
		case "4":
			vlanMenu(reader, dev, deviceName)
		case "5":
			aclMenu(reader, dev, deviceName)
		case "6":
			evpnMenu(reader, dev, deviceName)
		case "7":
			bgpMenu(reader, dev, deviceName)
		case "8":
			healthMenu(reader, dev, deviceName)
		case "9":
			fmt.Println("Baseline management - use 'newtron baseline' commands")
		case "q", "Q", "quit", "exit":
			fmt.Println("Goodbye!")
			return
		default:
			fmt.Println(red("Invalid option"))
		}
	}
}

func serviceMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("Service Management"))
		fmt.Println("  1. List available services")
		fmt.Println("  2. Apply service to interface")
		fmt.Println("  3. Remove service from interface")
		fmt.Println("  4. Show service details")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			services := net.ListServices()
			fmt.Println("\nAvailable services:")
			for _, name := range services {
				svc, _ := net.GetService(name)
				if svc != nil {
					fmt.Printf("  %s - %s (%s)\n", name, svc.Description, svc.ServiceType)
				}
			}
		case "2":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("Interface name: ")
			intf, _ := reader.ReadString('\n')
			intf = strings.TrimSpace(intf)

			fmt.Print("Service name: ")
			svc, _ := reader.ReadString('\n')
			svc = strings.TrimSpace(svc)

			fmt.Print("IP address (optional, for L3): ")
			ip, _ := reader.ReadString('\n')
			ip = strings.TrimSpace(ip)

			// Prompt for peer AS if service has routing.peer_as="request"
			var peerASNum int
			svcDef, svcErr := dev.Network().GetService(svc)
			if svcErr == nil && svcDef.Routing != nil && svcDef.Routing.PeerAS == "request" {
				fmt.Print("Enter BGP peer AS number: ")
				peerASStr, _ := reader.ReadString('\n')
				peerASStr = strings.TrimSpace(peerASStr)
				peerASNum, _ = strconv.Atoi(peerASStr)
			}

			// Perform the actual operation
			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			intfObj, err := dev.GetInterface(intf)
			if err != nil {
				dev.Unlock()
				fmt.Println(red("Interface not found: " + err.Error()))
				continue
			}

			cs, err := intfObj.ApplyService(ctx, svc, network.ApplyServiceOpts{
				IPAddress: ip,
				PeerAS:    peerASNum,
			})
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to apply service: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("Service applied successfully!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "3":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("Interface name: ")
			intf, _ := reader.ReadString('\n')
			intf = strings.TrimSpace(intf)

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			intfObj, err := dev.GetInterface(intf)
			if err != nil {
				dev.Unlock()
				fmt.Println(red("Interface not found: " + err.Error()))
				continue
			}

			cs, err := intfObj.RemoveService(ctx)
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to remove service: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("Service removed successfully!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "4":
			fmt.Print("Service name: ")
			name, _ := reader.ReadString('\n')
			name = strings.TrimSpace(name)

			svc, err := net.GetService(name)
			if err != nil {
				fmt.Println(red("Service not found"))
				continue
			}
			fmt.Printf("\nService: %s\n", bold(name))
			fmt.Printf("Type: %s\n", svc.ServiceType)
			fmt.Printf("Description: %s\n", svc.Description)
			if svc.IngressFilter != "" {
				fmt.Printf("Ingress Filter: %s\n", svc.IngressFilter)
			}
			if svc.EgressFilter != "" {
				fmt.Printf("Egress Filter: %s\n", svc.EgressFilter)
			}

		case "b", "B", "back":
			return
		}
	}
}

func interfaceMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("Interface Management"))
		fmt.Println("  1. List interfaces")
		fmt.Println("  2. Show interface details")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			interfaceNames := dev.ListInterfaces()
			fmt.Println("\nInterfaces:")
			for _, name := range interfaceNames {
				intf, err := dev.GetInterface(name)
				if err != nil {
					continue
				}
				status := intf.AdminStatus()
				if status == "up" {
					status = green("up")
				} else if status != "" {
					status = red(status)
				}

				ipAddr := "-"
				if addrs := intf.IPAddresses(); len(addrs) > 0 {
					ipAddr = strings.Join(addrs, ",")
				}

				fmt.Printf("  %s: %s %s %s\n", name, status, intf.Speed(), ipAddr)
			}

		case "2":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("Interface name: ")
			name, _ := reader.ReadString('\n')
			name = strings.TrimSpace(name)

			intf, err := dev.GetInterface(name)
			if err != nil {
				fmt.Println(red("Interface not found"))
				continue
			}
			fmt.Printf("\nInterface: %s\n", bold(name))
			fmt.Printf("Status: %s\n", intf.AdminStatus())
			fmt.Printf("Speed: %s\n", intf.Speed())
			if addrs := intf.IPAddresses(); len(addrs) > 0 {
				fmt.Printf("IP: %v\n", addrs)
			}
			if vrf := intf.VRF(); vrf != "" {
				fmt.Printf("VRF: %s\n", vrf)
			}

		case "b", "B", "back":
			return
		}
	}
}

func lagMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("LAG Management"))
		fmt.Println("  1. List LAGs")
		fmt.Println("  2. Create LAG")
		fmt.Println("  3. Add member")
		fmt.Println("  4. Remove member")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			portChannels := dev.ListPortChannels()
			if len(portChannels) == 0 {
				fmt.Println("No LAGs configured")
				continue
			}
			fmt.Println("\nLAGs:")
			for _, name := range portChannels {
				pc, _ := dev.GetPortChannel(name)
				if pc != nil {
					fmt.Printf("  %s: members=%v active=%d/%d\n",
						name, pc.Members, len(pc.ActiveMembers), len(pc.Members))
				}
			}

		case "2":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("LAG name (e.g., PortChannel100): ")
			lagName, _ := reader.ReadString('\n')
			lagName = strings.TrimSpace(lagName)

			fmt.Print("Member interfaces (comma-separated, e.g., Ethernet0,Ethernet4): ")
			membersStr, _ := reader.ReadString('\n')
			membersStr = strings.TrimSpace(membersStr)
			members := strings.Split(membersStr, ",")
			for i := range members {
				members[i] = strings.TrimSpace(members[i])
			}

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.CreatePortChannel(ctx, lagName, network.PortChannelConfig{
				Members: members,
			})
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to create LAG: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("LAG created successfully!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "3":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("LAG name: ")
			lagName, _ := reader.ReadString('\n')
			lagName = strings.TrimSpace(lagName)

			fmt.Print("Member interface to add: ")
			member, _ := reader.ReadString('\n')
			member = strings.TrimSpace(member)

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.AddPortChannelMember(ctx, lagName, member)
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to add member: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("Member added successfully!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "4":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("LAG name: ")
			lagName, _ := reader.ReadString('\n')
			lagName = strings.TrimSpace(lagName)

			fmt.Print("Member interface to remove: ")
			member, _ := reader.ReadString('\n')
			member = strings.TrimSpace(member)

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.RemovePortChannelMember(ctx, lagName, member)
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to remove member: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("Member removed successfully!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "b", "B", "back":
			return
		}
	}
}

func vlanMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("VLAN Management"))
		fmt.Println("  1. List VLANs")
		fmt.Println("  2. Create VLAN")
		fmt.Println("  3. Add port to VLAN")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			vlanIDs := dev.ListVLANs()
			if len(vlanIDs) == 0 {
				fmt.Println("No VLANs configured")
				continue
			}
			fmt.Println("\nVLANs:")
			for _, id := range vlanIDs {
				vlan, _ := dev.GetVLAN(id)
				if vlan != nil {
					vni := ""
					if vlan.L2VNI() > 0 {
						vni = fmt.Sprintf(" (VNI: %d)", vlan.L2VNI())
					}
					fmt.Printf("  %d:%s ports=%v\n", id, vni, vlan.Ports)
				}
			}

		case "2":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			vlanID, err := readInt(reader, "VLAN ID: ")
			if err != nil {
				fmt.Println(red("Invalid VLAN ID"))
				continue
			}

			fmt.Print("Description (optional): ")
			desc, _ := reader.ReadString('\n')
			desc = strings.TrimSpace(desc)

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.CreateVLAN(ctx, vlanID, network.VLANConfig{
				Description: desc,
			})
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to create VLAN: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("VLAN created successfully!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "3":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			vlanID, err := readInt(reader, "VLAN ID: ")
			if err != nil {
				fmt.Println(red("Invalid VLAN ID"))
				continue
			}

			fmt.Print("Port name: ")
			port, _ := reader.ReadString('\n')
			port = strings.TrimSpace(port)

			fmt.Print("Tagged? [y/N]: ")
			taggedStr, _ := reader.ReadString('\n')
			taggedStr = strings.TrimSpace(strings.ToLower(taggedStr))
			tagged := taggedStr == "y" || taggedStr == "yes"

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.AddVLANMember(ctx, vlanID, port, tagged)
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to add port: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("Port added to VLAN successfully!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "b", "B", "back":
			return
		}
	}
}

func healthMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("Health Checks"))
		fmt.Println("  1. Run all health checks")
		fmt.Println("  2. Run specific check")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Printf("Run: newtron -d %s health check\n", deviceName)

		case "2":
			fmt.Println("Available checks: interfaces, lag, bgp, vxlan, evpn")
			fmt.Print("Check name: ")
			name, _ := reader.ReadString('\n')
			name = strings.TrimSpace(name)
			if dev != nil {
				fmt.Printf("Run: newtron -d %s health check --check %s\n", deviceName, name)
			}

		case "b", "B", "back":
			return
		}
	}
}

func aclMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("ACL/Filter Management"))
		fmt.Println("  1. List ACL tables")
		fmt.Println("  2. List filter specs (from config)")
		fmt.Println("  3. Show ACL table details")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			configDB := dev.ConfigDB()
			if configDB == nil || len(configDB.ACLTable) == 0 {
				fmt.Println("No ACL tables configured")
				continue
			}
			fmt.Println("\nACL Tables:")
			for name, table := range configDB.ACLTable {
				fmt.Printf("  %s: type=%s stage=%s ports=%s\n",
					name, table.Type, table.Stage, table.Ports)
			}

		case "2":
			filterSpecs := net.ListFilterSpecs()
			if len(filterSpecs) == 0 {
				fmt.Println("No filter specs defined")
				continue
			}
			fmt.Println("\nFilter Specs:")
			for _, name := range filterSpecs {
				spec, _ := net.GetFilterSpec(name)
				if spec != nil {
					fmt.Printf("  %s: %s (%s, %d rules)\n",
						name, spec.Description, spec.Type, len(spec.Rules))
				}
			}

		case "3":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("ACL table name: ")
			name, _ := reader.ReadString('\n')
			name = strings.TrimSpace(name)

			configDB := dev.ConfigDB()
			if configDB == nil {
				fmt.Println(red("Not connected"))
				continue
			}
			table, ok := configDB.ACLTable[name]
			if !ok {
				fmt.Println(red("ACL table not found"))
				continue
			}
			fmt.Printf("\nACL Table: %s\n", bold(name))
			fmt.Printf("Type: %s\n", table.Type)
			fmt.Printf("Stage: %s\n", table.Stage)
			fmt.Printf("Ports: %s\n", table.Ports)
			fmt.Printf("Description: %s\n", table.PolicyDesc)

			// Show rules
			fmt.Println("\nRules:")
			for ruleKey, rule := range configDB.ACLRule {
				if strings.HasPrefix(ruleKey, name+"|") {
					ruleName := strings.TrimPrefix(ruleKey, name+"|")
					fmt.Printf("  %s: priority=%s action=%s\n",
						ruleName, rule.Priority, rule.PacketAction)
				}
			}

		case "b", "B", "back":
			return
		}
	}
}

func evpnMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("EVPN Management"))
		fmt.Println("  1. Show VTEP status")
		fmt.Println("  2. List VNI mappings")
		fmt.Println("  3. List VRFs with EVPN")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			configDB := dev.ConfigDB()
			if configDB == nil || len(configDB.VXLANTunnel) == 0 {
				fmt.Println("No VTEP configured")
				continue
			}
			fmt.Println("\nVTEP Configuration:")
			for name, vtep := range configDB.VXLANTunnel {
				fmt.Printf("  %s: src_ip=%s\n", name, vtep.SrcIP)
			}

		case "2":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			configDB := dev.ConfigDB()
			if configDB == nil || len(configDB.VXLANTunnelMap) == 0 {
				fmt.Println("No VNI mappings")
				continue
			}
			fmt.Println("\nVNI Mappings:")
			for key, mapping := range configDB.VXLANTunnelMap {
				if mapping.VLAN != "" {
					fmt.Printf("  %s: VNI %s -> VLAN %s (L2)\n", key, mapping.VNI, mapping.VLAN)
				} else if mapping.VRF != "" {
					fmt.Printf("  %s: VNI %s -> VRF %s (L3)\n", key, mapping.VNI, mapping.VRF)
				}
			}

		case "3":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			vrfNames := dev.ListVRFs()
			if len(vrfNames) == 0 {
				fmt.Println("No VRFs configured")
				continue
			}
			fmt.Println("\nVRFs:")
			for _, name := range vrfNames {
				vrf, _ := dev.GetVRF(name)
				if vrf != nil {
					vni := "-"
					if vrf.L3VNI > 0 {
						vni = fmt.Sprintf("%d", vrf.L3VNI)
					}
					fmt.Printf("  %s: L3VNI=%s interfaces=%v\n", name, vni, vrf.Interfaces)
				}
			}

		case "b", "B", "back":
			return
		}
	}
}

func bgpMenu(reader *bufio.Reader, dev *network.Device, deviceName string) {
	for {
		fmt.Println()
		fmt.Println(bold("BGP Management"))
		fmt.Println("  1. Show BGP summary")
		fmt.Println("  2. List BGP neighbors")
		fmt.Println("  3. Setup EVPN (with route reflectors)")
		fmt.Println("  4. Add neighbor")
		fmt.Println("  5. Remove neighbor")
		fmt.Println("  b. Back")
		fmt.Print("Select: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			resolved := dev.Resolved()
			fmt.Printf("\nBGP Summary for %s\n", bold(deviceName))
			fmt.Printf("Local AS: %d\n", resolved.ASNumber)
			fmt.Printf("Router ID: %s\n", resolved.RouterID)
			configDB := dev.ConfigDB()
			if configDB != nil {
				fmt.Printf("Neighbors: %d\n", len(configDB.BGPNeighbor))
			}

		case "2":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			configDB := dev.ConfigDB()
			if configDB == nil || len(configDB.BGPNeighbor) == 0 {
				fmt.Println("No BGP neighbors configured")
				continue
			}
			fmt.Println("\nBGP Neighbors:")
			for addr, neighbor := range configDB.BGPNeighbor {
				status := neighbor.AdminStatus
				if status == "" {
					status = "up"
				}
				fmt.Printf("  %s: ASN=%s desc=%s status=%s\n",
					addr, neighbor.ASN, neighbor.Name, status)
			}

		case "3":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.SetupBGPEVPN(ctx)
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to setup EVPN: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("BGP EVPN setup completed!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "4":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("Neighbor IP: ")
			neighborIP, _ := reader.ReadString('\n')
			neighborIP = strings.TrimSpace(neighborIP)

			asn, err := readInt(reader, "ASN: ")
			if err != nil {
				fmt.Println(red("Invalid ASN"))
				continue
			}

			fmt.Print("Description (optional): ")
			desc, _ := reader.ReadString('\n')
			desc = strings.TrimSpace(desc)

			fmt.Print("Enable EVPN? [y/N]: ")
			evpnStr, _ := reader.ReadString('\n')
			evpnStr = strings.TrimSpace(strings.ToLower(evpnStr))
			evpn := evpnStr == "y" || evpnStr == "yes"

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.AddBGPNeighbor(ctx, neighborIP, asn, desc, evpn)
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to add neighbor: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("BGP neighbor added!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "5":
			if dev == nil {
				fmt.Println(red("No device connected"))
				continue
			}
			fmt.Print("Neighbor IP: ")
			neighborIP, _ := reader.ReadString('\n')
			neighborIP = strings.TrimSpace(neighborIP)

			ctx := context.Background()
			if err := dev.Lock(); err != nil {
				fmt.Println(red("Failed to lock device: " + err.Error()))
				continue
			}

			cs, err := dev.RemoveBGPNeighbor(ctx, neighborIP)
			dev.Unlock()
			if err != nil {
				fmt.Println(red("Failed to remove neighbor: " + err.Error()))
				continue
			}

			fmt.Println("\nChanges to apply:")
			fmt.Println(cs.Preview())

			fmt.Print("\nExecute? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := cs.Apply(dev); err != nil {
					fmt.Println(red("Failed to apply changes: " + err.Error()))
				} else {
					fmt.Println(green("BGP neighbor removed!"))
				}
			} else {
				fmt.Println("Cancelled.")
			}

		case "b", "B", "back":
			return
		}
	}
}

func readInt(reader *bufio.Reader, prompt string) (int, error) {
	fmt.Print(prompt)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	return strconv.Atoi(input)
}
