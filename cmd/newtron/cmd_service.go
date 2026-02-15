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
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

var (
	svcCreateType          string
	svcCreateIPVPN         string
	svcCreateMACVPN        string
	svcCreateVRFType       string
	svcCreateVLAN          int
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
  - QoS profiles

Examples:
  newtron service list                                        # List all services
  newtron service show customer-l3                            # Show service details
  newtron -d leaf1-ny service apply Ethernet0 customer-l3 --ip 10.1.1.1/30
  newtron -d leaf1-ny service remove Ethernet0`,
}

var serviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available services",
	RunE: func(cmd *cobra.Command, args []string) error {
		services := app.net.ListServices()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(services)
		}

		if len(services) == 0 {
			fmt.Println("No services defined")
			return nil
		}

		t := cli.NewTable("NAME", "TYPE", "DESCRIPTION")

		for _, name := range services {
			svc, _ := app.net.GetService(name)
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

		svc, err := app.net.GetService(serviceName)
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
		var ipvpnDef *spec.IPVPNSpec
		var macvpnDef *spec.MACVPNSpec
		if svc.IPVPN != "" {
			ipvpnDef, _ = app.net.GetIPVPN(svc.IPVPN)
		}
		if svc.MACVPN != "" {
			macvpnDef, _ = app.net.GetMACVPN(svc.MACVPN)
		}

		switch svc.ServiceType {
		case spec.ServiceTypeL2:
			fmt.Println("L2 Configuration:")
			if svc.MACVPN != "" {
				fmt.Printf("  MAC-VPN: %s\n", svc.MACVPN)
			}
			if svc.VLAN > 0 {
				fmt.Printf("  VLAN: %d\n", svc.VLAN)
			}
			if macvpnDef != nil {
				if macvpnDef.L2VNI > 0 {
					fmt.Printf("  L2VNI: %d\n", macvpnDef.L2VNI)
				}
				if macvpnDef.ARPSuppression {
					fmt.Println("  ARP Suppression: enabled")
				}
			}

		case spec.ServiceTypeL3:
			fmt.Println("L3 Configuration:")
			if svc.VRFType != "" {
				fmt.Printf("  VRF Type: %s\n", svc.VRFType)
			}
			if svc.IPVPN != "" {
				fmt.Printf("  IP-VPN: %s\n", svc.IPVPN)
			}
			if ipvpnDef != nil {
				if ipvpnDef.L3VNI > 0 {
					fmt.Printf("  L3VNI: %d\n", ipvpnDef.L3VNI)
				}
				if len(ipvpnDef.ImportRT) > 0 {
					fmt.Printf("  Import RT: %v\n", ipvpnDef.ImportRT)
				}
				if len(ipvpnDef.ExportRT) > 0 {
					fmt.Printf("  Export RT: %v\n", ipvpnDef.ExportRT)
				}
			}

		case spec.ServiceTypeIRB:
			fmt.Println("IRB Configuration:")
			if svc.MACVPN != "" {
				fmt.Printf("  MAC-VPN: %s\n", svc.MACVPN)
			}
			if svc.VLAN > 0 {
				fmt.Printf("  VLAN: %d\n", svc.VLAN)
			}
			if macvpnDef != nil {
				if macvpnDef.L2VNI > 0 {
					fmt.Printf("  L2VNI: %d\n", macvpnDef.L2VNI)
				}
			}
			if svc.VRFType != "" {
				fmt.Printf("  VRF Type: %s\n", svc.VRFType)
			}
			if svc.IPVPN != "" {
				fmt.Printf("  IP-VPN: %s\n", svc.IPVPN)
			}
			if ipvpnDef != nil && ipvpnDef.L3VNI > 0 {
				fmt.Printf("  L3VNI: %d\n", ipvpnDef.L3VNI)
			}
			if svc.AnycastGateway != "" {
				fmt.Printf("  Anycast Gateway: %s\n", svc.AnycastGateway)
			}
			if svc.AnycastMAC != "" {
				fmt.Printf("  Anycast MAC: %s\n", svc.AnycastMAC)
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
		} else if svc.QoSProfile != "" {
			fmt.Println("\nQoS:")
			fmt.Printf("  Profile: %s (legacy)\n", svc.QoSProfile)
		}

		return nil
	},
}

var (
	applyIP  string
	peerAS   int
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

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny service apply Ethernet0 customer-l3 --ip 10.1.1.1/30
  newtron -d leaf1-ny service apply PortChannel100 transit --ip 192.168.1.1/31 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		serviceName := args[1]
		return withDeviceWrite(func(ctx context.Context, dev *node.Node) (*node.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithService(serviceName).WithInterface(intfName)
			if err := checkExecutePermission(auth.PermServiceApply, authCtx); err != nil {
				return nil, err
			}

			intf, err := dev.GetInterface(intfName)
			if err != nil {
				return nil, fmt.Errorf("interface not found: %w", err)
			}

			// Show derived values
			svc, _ := app.net.GetService(serviceName)
			derived, _ := util.DeriveFromInterface(intfName, applyIP, serviceName)

			fmt.Printf("\nApplying service '%s' to interface %s...\n", serviceName, intfName)
			fmt.Println("\nDerived configuration:")
			if applyIP != "" && derived != nil {
				if derived.NeighborIP != "" {
					fmt.Printf("  Neighbor IP: %s\n", derived.NeighborIP)
				}
				fmt.Printf("  VRF Name: %s\n", derived.VRFName)
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

			cs, err := intf.ApplyService(ctx, serviceName, node.ApplyServiceOpts{
				IPAddress: applyIP,
				PeerAS:    peerAS,
			})
			if err != nil {
				return nil, fmt.Errorf("applying service: %w", err)
			}
			return cs, nil
		})
	},
}

var serviceRemoveCmd = &cobra.Command{
	Use:   "remove <interface>",
	Short: "Remove a service from an interface",
	Long: `Remove a service from an interface.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny service remove Ethernet0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		return withDeviceWrite(func(ctx context.Context, dev *node.Node) (*node.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithInterface(intfName)
			if err := checkExecutePermission(auth.PermServiceRemove, authCtx); err != nil {
				return nil, err
			}
			intf, err := dev.GetInterface(intfName)
			if err != nil {
				return nil, fmt.Errorf("interface not found: %w", err)
			}
			cs, err := intf.RemoveService(ctx)
			if err != nil {
				return nil, fmt.Errorf("removing service: %w", err)
			}
			return cs, nil
		})
	},
}

var serviceGetCmd = &cobra.Command{
	Use:   "get <interface>",
	Short: "Get the service bound to an interface",
	Long: `Get the service bound to an interface.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny service get Ethernet0`,
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

		svc := intf.ServiceName()
		if svc == "" {
			fmt.Println("(no service bound)")
			return nil
		}

		if app.jsonOutput {
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

var serviceRefreshCmd = &cobra.Command{
	Use:   "refresh <interface>",
	Short: "Refresh the service on an interface",
	Long: `Refresh the service on an interface to match the current service definition.

Use this when the service definition (filters, QoS, etc.) has changed in network.json
and you want to sync the interface configuration to match.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny service refresh Ethernet0 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		return withDeviceWrite(func(ctx context.Context, dev *node.Node) (*node.ChangeSet, error) {
			intf, err := dev.GetInterface(intfName)
			if err != nil {
				return nil, fmt.Errorf("interface not found: %w", err)
			}
			if !intf.HasService() {
				return nil, fmt.Errorf("no service bound to interface %s", intfName)
			}

			serviceName := intf.ServiceName()
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(intfName).WithService(serviceName)
			if err := checkExecutePermission(auth.PermServiceApply, authCtx); err != nil {
				return nil, err
			}

			cs, err := intf.RefreshService(ctx)
			if err != nil {
				return nil, fmt.Errorf("refreshing service: %w", err)
			}

			if cs.IsEmpty() {
				fmt.Println("Service is already in sync with definition. No changes needed.")
				return nil, nil
			}

			// Show orphaned ACLs that will be cleaned up
			orphans := dev.GetOrphanedACLs()
			if len(orphans) > 0 {
				fmt.Println("Orphaned ACLs to be removed:")
				for _, acl := range orphans {
					fmt.Printf("  - %s\n", acl)
				}
				fmt.Println()
			}

			return cs, nil
		})
	},
}

var serviceCreateCmd = &cobra.Command{
	Use:   "create <service-name>",
	Short: "Create a new service definition",
	Long: `Create a new service definition in network.json.

This is a spec-level command (no device needed). The service can then be
applied to interfaces on devices.

Flags:
  --type          Service type: l2, l3, or irb (required)
  --ipvpn         IP-VPN reference name
  --macvpn        MAC-VPN reference name
  --vrf-type      VRF instantiation: "interface" or "shared"
  --vlan          VLAN ID for L2/IRB services
  --qos-policy    QoS policy name
  --ingress-filter Ingress filter spec name
  --egress-filter  Egress filter spec name
  --unnumbered    Use unnumbered interface (L3)
  --description   Service description

Examples:
  newtron service create customer-l3 --type l3 --ipvpn cust-vpn --vrf-type shared --description "Customer L3 VPN"
  newtron service create server-l2 --type l2 --macvpn servers --vlan 100
  newtron service create fabric-irb --type irb --ipvpn fabric --macvpn fabric-l2 --vlan 200`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]

		if svcCreateType == "" {
			return fmt.Errorf("--type is required (l2, l3, irb)")
		}
		if svcCreateType != spec.ServiceTypeL2 && svcCreateType != spec.ServiceTypeL3 && svcCreateType != spec.ServiceTypeIRB {
			return fmt.Errorf("--type must be 'l2', 'l3', or 'irb', got '%s'", svcCreateType)
		}

		// Check if already exists
		if _, err := app.net.GetService(serviceName); err == nil {
			return fmt.Errorf("service '%s' already exists", serviceName)
		}

		authCtx := auth.NewContext().WithResource(serviceName)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		svc := &spec.ServiceSpec{
			Description:   svcCreateDescription,
			ServiceType:   svcCreateType,
			IPVPN:         svcCreateIPVPN,
			MACVPN:        svcCreateMACVPN,
			VRFType:       svcCreateVRFType,
			VLAN:          svcCreateVLAN,
			QoSPolicy:     svcCreateQoSPolicy,
			IngressFilter: svcCreateIngressFilter,
			EgressFilter:  svcCreateEgressFilter,
		}

		fmt.Printf("Service: %s (type: %s)\n", serviceName, svcCreateType)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.SaveService(serviceName, svc); err != nil {
			return fmt.Errorf("saving service: %w", err)
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

		// Verify it exists
		if _, err := app.net.GetService(serviceName); err != nil {
			return err
		}

		authCtx := auth.NewContext().WithResource(serviceName)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		fmt.Printf("Deleting service: %s\n", serviceName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.DeleteService(serviceName); err != nil {
			return err
		}

		fmt.Printf("Deleted service '%s'\n", serviceName)
		return nil
	},
}

func init() {
	serviceApplyCmd.Flags().StringVar(&applyIP, "ip", "", "IP address for L3 service (CIDR notation)")
	serviceApplyCmd.Flags().IntVar(&peerAS, "peer-as", 0, "BGP peer AS number")

	serviceCreateCmd.Flags().StringVar(&svcCreateType, "type", "", "Service type (l2, l3, irb)")
	serviceCreateCmd.Flags().StringVar(&svcCreateIPVPN, "ipvpn", "", "IP-VPN reference name")
	serviceCreateCmd.Flags().StringVar(&svcCreateMACVPN, "macvpn", "", "MAC-VPN reference name")
	serviceCreateCmd.Flags().StringVar(&svcCreateVRFType, "vrf-type", "", "VRF instantiation type (interface, shared)")
	serviceCreateCmd.Flags().IntVar(&svcCreateVLAN, "vlan", 0, "VLAN ID for L2/IRB services")
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
