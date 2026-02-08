package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/audit"
	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
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
		services := net.ListServices()

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(services)
		}

		if len(services) == 0 {
			fmt.Println("No services defined")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tDESCRIPTION")
		fmt.Fprintln(w, "----\t----\t-----------")

		for _, name := range services {
			svc, _ := net.GetService(name)
			if svc != nil {
				fmt.Fprintf(w, "%s\t%s\t%s\n", name, svc.ServiceType, svc.Description)
			}
		}
		w.Flush()

		return nil
	},
}

var serviceShowCmd = &cobra.Command{
	Use:   "show <service-name>",
	Short: "Show service details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName := args[0]

		svc, err := net.GetService(serviceName)
		if err != nil {
			return err
		}

		if jsonOutput {
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
			ipvpnDef, _ = net.GetIPVPN(svc.IPVPN)
		}
		if svc.MACVPN != "" {
			macvpnDef, _ = net.GetMACVPN(svc.MACVPN)
		}

		switch svc.ServiceType {
		case spec.ServiceTypeL2:
			fmt.Println("L2 Configuration:")
			if svc.MACVPN != "" {
				fmt.Printf("  MAC-VPN: %s\n", svc.MACVPN)
			}
			if macvpnDef != nil {
				fmt.Printf("  VLAN: %d\n", macvpnDef.VLAN)
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
			if macvpnDef != nil {
				fmt.Printf("  VLAN: %d\n", macvpnDef.VLAN)
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

		if svc.QoSProfile != "" {
			fmt.Println("\nQoS:")
			fmt.Printf("  Profile: %s\n", svc.QoSProfile)
			fmt.Printf("  Trust DSCP: %v\n", svc.TrustDSCP)
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
		intfArg := args[0]
		serviceName := args[1]

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		// Check permissions
		authCtx := auth.NewContext().WithDevice(deviceName).WithService(serviceName).WithInterface(intfArg)
		if err := checkExecutePermission(auth.PermServiceApply, authCtx); err != nil {
			return err
		}

		// Lock device for changes
		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Get interface object (OO style)
		intf, err := dev.GetInterface(intfArg)
		if err != nil {
			return fmt.Errorf("interface not found: %w", err)
		}

		// Show derived values
		svc, _ := net.GetService(serviceName)
		derived, _ := util.DeriveFromInterface(intfArg, applyIP, serviceName)

		fmt.Printf("\nApplying service '%s' to interface %s...\n", serviceName, intfArg)
		fmt.Println("\nDerived configuration:")
		if applyIP != "" && derived != nil {
			if derived.NeighborIP != "" {
				fmt.Printf("  Neighbor IP: %s\n", derived.NeighborIP)
			}
			fmt.Printf("  VRF Name: %s\n", derived.VRFName)
		}
		if svc != nil {
			if svc.IngressFilter != "" {
				fmt.Printf("  Ingress ACL: %s-in\n", derived.ACLNameBase)
			}
			if svc.EgressFilter != "" {
				fmt.Printf("  Egress ACL: %s-out\n", derived.ACLNameBase)
			}
		}

		// Apply service to interface (OO style - method on interface)
		start := time.Now()
		changeSet, err := intf.ApplyService(ctx, serviceName, network.ApplyServiceOpts{
			IPAddress: applyIP,
			PeerAS:    peerAS,
		})
		if err != nil {
			return fmt.Errorf("applying service: %w", err)
		}

		// Show changes
		fmt.Println("\nChanges to be applied:")
		if changeSet.IsEmpty() {
			fmt.Println("  (no changes)")
		} else {
			fmt.Print(changeSet.String())
		}

		// Execute if -x flag
		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				// Log failure
				audit.Log(audit.NewEvent(permChecker.CurrentUser(), deviceName, "interface.apply-service").
					WithService(serviceName).
					WithInterface(intfArg).
					WithError(err).
					WithExecuteMode(true).
					WithDuration(time.Since(start)))

				return err
			}

			// Log success
			audit.Log(audit.NewEvent(permChecker.CurrentUser(), deviceName, "interface.apply-service").
				WithService(serviceName).
				WithInterface(intfArg).
				WithSuccess().
				WithExecuteMode(true).
				WithDuration(time.Since(start)))
		} else {
			printDryRunNotice()
		}

		return nil
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
		intfArg := args[0]

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		// Check permissions
		authCtx := auth.NewContext().WithDevice(deviceName).WithInterface(intfArg)
		if err := checkExecutePermission(auth.PermServiceRemove, authCtx); err != nil {
			return err
		}

		// Lock device
		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Get interface object (OO style)
		intf, err := dev.GetInterface(intfArg)
		if err != nil {
			return fmt.Errorf("interface not found: %w", err)
		}

		// Remove service from interface (OO style)
		changeSet, err := intf.RemoveService(ctx)
		if err != nil {
			return fmt.Errorf("removing service: %w", err)
		}

		fmt.Println("Changes to be applied:")
		if changeSet.IsEmpty() {
			fmt.Println("  (no changes)")
		} else {
			fmt.Print(changeSet.String())
		}

		// Execute if -x
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

func init() {
	serviceApplyCmd.Flags().StringVar(&applyIP, "ip", "", "IP address for L3 service (CIDR notation)")
	serviceApplyCmd.Flags().IntVar(&peerAS, "peer-as", 0, "BGP peer AS number")

	serviceCmd.AddCommand(serviceListCmd)
	serviceCmd.AddCommand(serviceShowCmd)
	serviceCmd.AddCommand(serviceApplyCmd)
	serviceCmd.AddCommand(serviceRemoveCmd)
}
