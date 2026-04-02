package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/util"
)

var (
	svcCreateType          string
	svcCreateIPVPN         string
	svcCreateMACVPN        string
	svcCreateVRFType       string
	svcCreateQoSPolicy     string
	svcCreateIngressFilter string
	svcCreateEgressFilter  string
	svcCreateDescription   string
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage interface services",
	Long: `Manage interface services.

Services define a complete interface configuration including:
  - VRF/EVPN settings
  - ACL/filter rules
  - QoS policies

Examples:
  newtron service list                                        # List all services
  newtron service show customer-l3                            # Show service details
  newtron -D leaf1-ny service apply Ethernet0 customer-l3 --ip 10.1.1.1/30
  newtron -D leaf1-ny service remove Ethernet0`,
}

var serviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available services",
	RunE: func(cmd *cobra.Command, args []string) error {
		services, err := app.client.ListServices()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(services)
		}

		if len(services) == 0 {
			fmt.Println("No services defined")
			return nil
		}

		t := cli.NewTable("NAME", "TYPE", "DESCRIPTION")

		for _, name := range services {
			svc, _ := app.client.ShowService(name)
			if svc != nil {
				t.Row(name, svc.ServiceType, svc.Description)
			}
		}
		t.Flush()

		return nil
	},
}

var serviceShowCmd = &cobra.Command{
	Use:   "show <service-name>",
	Short: "Show service details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]

		svc, err := app.client.ShowService(serviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(svc)
		}

		fmt.Printf("Service: %s\n", bold(serviceName))
		fmt.Printf("Description: %s\n", svc.Description)
		fmt.Printf("Type: %s\n", svc.ServiceType)
		fmt.Println()

		// Look up VPN definitions
		var ipvpnDef *newtron.IPVPNDetail
		var macvpnDef *newtron.MACVPNDetail
		if svc.IPVPN != "" {
			ipvpnDef, _ = app.client.ShowIPVPN(svc.IPVPN)
		}
		if svc.MACVPN != "" {
			macvpnDef, _ = app.client.ShowMACVPN(svc.MACVPN)
		}

		switch svc.ServiceType {
		case newtron.ServiceTypeEVPNIRB:
			fmt.Println("EVPN IRB Configuration:")
			if svc.MACVPN != "" {
				fmt.Printf("  MAC-VPN: %s\n", svc.MACVPN)
			}
			if macvpnDef != nil {
				if macvpnDef.VlanID > 0 {
					fmt.Printf("  VLAN: %d\n", macvpnDef.VlanID)
				}
				if macvpnDef.VNI > 0 {
					fmt.Printf("  L2VNI: %d\n", macvpnDef.VNI)
				}
				if macvpnDef.AnycastIP != "" {
					fmt.Printf("  Anycast Gateway: %s\n", macvpnDef.AnycastIP)
				}
				if macvpnDef.AnycastMAC != "" {
					fmt.Printf("  Anycast MAC: %s\n", macvpnDef.AnycastMAC)
				}
				if macvpnDef.ARPSuppression {
					fmt.Println("  ARP Suppression: enabled")
				}
			}
			if svc.IPVPN != "" {
				fmt.Printf("  IP-VPN: %s\n", svc.IPVPN)
			}
			if ipvpnDef != nil {
				if ipvpnDef.VRF != "" {
					fmt.Printf("  VRF: %s\n", ipvpnDef.VRF)
				}
				if ipvpnDef.L3VNI > 0 {
					fmt.Printf("  L3VNI: %d\n", ipvpnDef.L3VNI)
				}
				if len(ipvpnDef.RouteTargets) > 0 {
					fmt.Printf("  Route Targets: %v\n", ipvpnDef.RouteTargets)
				}
			}
			if svc.VRFType != "" {
				fmt.Printf("  VRF Type: %s\n", svc.VRFType)
			}

		case newtron.ServiceTypeEVPNBridged:
			fmt.Println("EVPN Bridged Configuration:")
			if svc.MACVPN != "" {
				fmt.Printf("  MAC-VPN: %s\n", svc.MACVPN)
			}
			if macvpnDef != nil {
				if macvpnDef.VlanID > 0 {
					fmt.Printf("  VLAN: %d\n", macvpnDef.VlanID)
				}
				if macvpnDef.VNI > 0 {
					fmt.Printf("  L2VNI: %d\n", macvpnDef.VNI)
				}
				if macvpnDef.ARPSuppression {
					fmt.Println("  ARP Suppression: enabled")
				}
			}

		case newtron.ServiceTypeEVPNRouted:
			fmt.Println("EVPN Routed Configuration:")
			if svc.IPVPN != "" {
				fmt.Printf("  IP-VPN: %s\n", svc.IPVPN)
			}
			if ipvpnDef != nil {
				if ipvpnDef.VRF != "" {
					fmt.Printf("  VRF: %s\n", ipvpnDef.VRF)
				}
				if ipvpnDef.L3VNI > 0 {
					fmt.Printf("  L3VNI: %d\n", ipvpnDef.L3VNI)
				}
				if len(ipvpnDef.RouteTargets) > 0 {
					fmt.Printf("  Route Targets: %v\n", ipvpnDef.RouteTargets)
				}
			}
			if svc.VRFType != "" {
				fmt.Printf("  VRF Type: %s\n", svc.VRFType)
			}

		case newtron.ServiceTypeIRB:
			fmt.Println("Local IRB Configuration:")
			if svc.VRFType != "" {
				fmt.Printf("  VRF Type: %s\n", svc.VRFType)
			}

		case newtron.ServiceTypeBridged:
			fmt.Println("Local L2 Configuration:")

		case newtron.ServiceTypeRouted:
			fmt.Println("Local L3 Configuration:")
			if svc.VRFType != "" {
				fmt.Printf("  VRF Type: %s\n", svc.VRFType)
			}
		}

		if svc.IngressFilter != "" || svc.EgressFilter != "" {
			fmt.Println("\nFilters:")
			if svc.IngressFilter != "" {
				fmt.Printf("  Ingress: %s\n", svc.IngressFilter)
			}
			if svc.EgressFilter != "" {
				fmt.Printf("  Egress: %s\n", svc.EgressFilter)
			}
		}

		if svc.QoSPolicy != "" {
			fmt.Println("\nQoS:")
			fmt.Printf("  Policy: %s\n", svc.QoSPolicy)
		}

		return nil
	},
}

var (
	applyIP     string
	applyVLAN   int
	applyParams string
	peerAS      int
)

var serviceApplyCmd = &cobra.Command{
	Use:   "apply <interface> <service>",
	Short: "Apply a service to an interface",
	Long: `Apply a service to an interface.

This operation configures:
  - VRF binding and EVPN settings
  - ACL rules from the service's filter specs
  - QoS profile

By default, this shows what would change (dry-run).
Use -x to actually apply the changes.

Requires -D (device) flag.

Options:
  --ip <addr/prefix>        IP address for routed/IRB services
  --vlan <id>               VLAN ID for local bridged/IRB services
  --peer-as <asn>           BGP peer AS number (for services with routing.peer_as="request")
  --params <key=val,...>    Topology params (peer_as, route_reflector_client, next_hop_self)

Examples:
  newtron leaf1 service apply Ethernet0 customer-l3 --ip 10.1.1.1/30 -x
  newtron leaf1 service apply Ethernet0 server-l2 --vlan 100 -x
  newtron leaf1 service apply Ethernet0 transit --ip 192.168.1.1/31 --params peer_as=65002 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		serviceName := args[1]

		if err := requireDevice(); err != nil {
			return err
		}

		svc, _ := app.client.ShowService(serviceName)
		derived, _ := util.DeriveFromInterface(intfName, applyIP, serviceName)

		fmt.Printf("\nApplying service '%s' to interface %s...\n", serviceName, intfName)
		fmt.Println("\nDerived configuration:")
		if applyIP != "" && derived != nil {
			if derived.NeighborIP != "" {
				fmt.Printf("  Neighbor IP: %s\n", derived.NeighborIP)
			}
			if svc != nil && svc.VRFType != "" {
				fmt.Printf("  VRF Name: %s\n", derived.VRFName)
			}
		}
		if svc != nil {
			if svc.IngressFilter != "" {
				fmt.Printf("  Ingress ACL: %s-in\n", derived.ACLPrefix)
			}
			if svc.EgressFilter != "" {
				fmt.Printf("  Egress ACL: %s-out\n", derived.ACLPrefix)
			}
		}
		fmt.Println()

		opts := newtron.ApplyServiceOpts{
			IPAddress: applyIP,
			VLAN:      applyVLAN,
			PeerAS:    peerAS,
		}
		if applyParams != "" {
			opts.Params = make(map[string]string)
			for _, kv := range strings.Split(applyParams, ",") {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) == 2 {
					opts.Params[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
			}
		}

		return displayWriteResult(app.client.ApplyService(app.deviceName, intfName, serviceName, opts, execOpts()))
	},
}

var serviceRemoveCmd = &cobra.Command{
	Use:   "remove <interface>",
	Short: "Remove a service from an interface",
	Long: `Remove a service from an interface.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny service remove Ethernet0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]

		if err := requireDevice(); err != nil {
			return err
		}

		return displayWriteResult(app.client.RemoveService(app.deviceName, intfName, execOpts()))
	},
}

var serviceGetCmd = &cobra.Command{
	Use:   "get <interface>",
	Short: "Get the service bound to an interface",
	Long: `Get the service bound to an interface.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny service get Ethernet0`,
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

		svc := detail.Service
		if svc == "" {
			fmt.Println("(no service bound)")
			return nil
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"service": svc,
				"ip":      strings.Join(detail.IPAddresses, ", "),
				"vrf":     detail.VRF,
			})
		}

		fmt.Printf("Service: %s\n", svc)
		if len(detail.IPAddresses) > 0 {
			fmt.Printf("IP: %s\n", strings.Join(detail.IPAddresses, ", "))
		}
		if detail.VRF != "" {
			fmt.Printf("VRF: %s\n", detail.VRF)
		}
		return nil
	},
}

var serviceRefreshCmd = &cobra.Command{
	Use:   "refresh <interface>",
	Short: "Refresh the service on an interface",
	Long: `Refresh the service on an interface to match the current service definition.

Use this when the service definition (filters, QoS, etc.) has changed in network.json
and you want to sync the interface configuration to match.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny service refresh Ethernet0 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]

		if err := requireDevice(); err != nil {
			return err
		}

		result, err := app.client.RefreshService(app.deviceName, intfName, execOpts())
		if err != nil {
			return err
		}

		if result != nil && result.ChangeCount == 0 {
			fmt.Println("already in sync")
			return nil
		}

		return displayWriteResult(result, nil)
	},
}

var serviceCreateCmd = &cobra.Command{
	Use:   "create <service-name>",
	Short: "Create a new service definition",
	Long: `Create a new service definition in network.json.

This is a spec-level command (no device needed). The service can then be
applied to interfaces on devices.

Flags:
  --type          Service type (required): evpn-irb, evpn-bridged, evpn-routed, irb, bridged, routed
  --ipvpn         IP-VPN reference name
  --macvpn        MAC-VPN reference name
  --vrf-type      VRF instantiation: "interface" or "shared"
  --qos-policy    QoS policy name
  --ingress-filter Ingress filter spec name
  --egress-filter  Egress filter spec name
  --description   Service description

Examples:
  newtron service create customer-l3 --type evpn-routed --ipvpn cust-vpn --vrf-type shared --description "Customer L3 VPN"
  newtron service create server-l2 --type evpn-bridged --macvpn servers
  newtron service create fabric-irb --type evpn-irb --ipvpn fabric --macvpn fabric-l2`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]

		if svcCreateType == "" {
			return fmt.Errorf("--type is required (evpn-irb, evpn-bridged, evpn-routed, irb, bridged, routed)")
		}

		validTypes := map[string]bool{
			newtron.ServiceTypeEVPNIRB:     true,
			newtron.ServiceTypeEVPNBridged: true,
			newtron.ServiceTypeEVPNRouted:  true,
			newtron.ServiceTypeIRB:         true,
			newtron.ServiceTypeBridged:     true,
			newtron.ServiceTypeRouted:      true,
		}
		if !validTypes[svcCreateType] {
			return fmt.Errorf("--type must be one of: evpn-irb, evpn-bridged, evpn-routed, irb, bridged, routed; got '%s'", svcCreateType)
		}

		fmt.Printf("Service: %s (type: %s)\n", serviceName, svcCreateType)

		if err := app.client.CreateService(newtron.CreateServiceRequest{
			Name:          serviceName,
			Type:          svcCreateType,
			IPVPN:         svcCreateIPVPN,
			MACVPN:        svcCreateMACVPN,
			VRFType:       svcCreateVRFType,
			QoSPolicy:     svcCreateQoSPolicy,
			IngressFilter: svcCreateIngressFilter,
			EgressFilter:  svcCreateEgressFilter,
			Description:   svcCreateDescription,
		}, execOpts()); err != nil {
			return fmt.Errorf("saving service: %w", err)
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Created service '%s' (type: %s)\n", serviceName, svcCreateType)
		return nil
	},
}

var serviceDeleteCmd = &cobra.Command{
	Use:   "delete <service-name>",
	Short: "Delete a service definition",
	Long: `Delete a service definition from network.json.

This is a spec-level command (no device needed).

Note: This does not remove the service from interfaces where it is currently
applied. Use 'service remove' on each interface first.

Examples:
  newtron service delete customer-l3`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]

		fmt.Printf("Deleting service: %s\n", serviceName)

		if err := app.client.DeleteService(serviceName, execOpts()); err != nil {
			return err
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Deleted service '%s'\n", serviceName)
		return nil
	},
}

func init() {
	serviceApplyCmd.Flags().StringVar(&applyIP, "ip", "", "IP address for L3 service (CIDR notation)")
	serviceApplyCmd.Flags().IntVar(&applyVLAN, "vlan", 0, "VLAN ID for local bridged/IRB services")
	serviceApplyCmd.Flags().IntVar(&peerAS, "peer-as", 0, "BGP peer AS number")
	serviceApplyCmd.Flags().StringVar(&applyParams, "params", "", "Topology params as key=value pairs (comma-separated)")

	serviceCreateCmd.Flags().StringVar(&svcCreateType, "type", "", "Service type (evpn-irb, evpn-bridged, evpn-routed, irb, bridged, routed)")
	serviceCreateCmd.Flags().StringVar(&svcCreateIPVPN, "ipvpn", "", "IP-VPN reference name")
	serviceCreateCmd.Flags().StringVar(&svcCreateMACVPN, "macvpn", "", "MAC-VPN reference name")
	serviceCreateCmd.Flags().StringVar(&svcCreateVRFType, "vrf-type", "", "VRF instantiation type (interface, shared)")
	serviceCreateCmd.Flags().StringVar(&svcCreateQoSPolicy, "qos-policy", "", "QoS policy name")
	serviceCreateCmd.Flags().StringVar(&svcCreateIngressFilter, "ingress-filter", "", "Ingress filter spec name")
	serviceCreateCmd.Flags().StringVar(&svcCreateEgressFilter, "egress-filter", "", "Egress filter spec name")
	serviceCreateCmd.Flags().StringVar(&svcCreateDescription, "description", "", "Service description")

	serviceCmd.AddCommand(serviceListCmd)
	serviceCmd.AddCommand(serviceShowCmd)
	serviceCmd.AddCommand(serviceGetCmd)
	serviceCmd.AddCommand(serviceApplyCmd)
	serviceCmd.AddCommand(serviceRemoveCmd)
	serviceCmd.AddCommand(serviceRefreshCmd)
	serviceCmd.AddCommand(serviceCreateCmd)
	serviceCmd.AddCommand(serviceDeleteCmd)
}
