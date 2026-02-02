package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var interfaceCmd = &cobra.Command{
	Use:   "interface",
	Short: "Manage interfaces",
	Long: `Manage device interfaces.

Requires -d (device) flag. Use -i (interface) for interface-specific commands.

Examples:
  newtron -d leaf1-ny interface list
  newtron -d leaf1-ny -i Ethernet0 interface show`,
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

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(interfaces)
		}

		if len(interfaces) == 0 {
			fmt.Println("No interfaces found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "INTERFACE\tADMIN\tOPER\tIP ADDRESS\tVRF\tSERVICE")
		fmt.Fprintln(w, "---------\t-----\t----\t----------\t---\t-------")

		for _, name := range interfaces {
			intf, err := dev.GetInterface(name)
			if err != nil {
				continue
			}

			adminStatus := intf.AdminStatus()
			if adminStatus == "up" {
				adminStatus = green("up")
			} else if adminStatus != "" {
				adminStatus = red(adminStatus)
			} else {
				adminStatus = "-"
			}

			operStatus := intf.OperStatus()
			if operStatus == "up" {
				operStatus = green("up")
			} else if operStatus != "" {
				operStatus = red(operStatus)
			} else {
				operStatus = "-"
			}

			ipAddr := "-"
			if addrs := intf.IPAddresses(); len(addrs) > 0 {
				ipAddr = strings.Join(addrs, ",")
			}

			vrf := intf.VRF()
			if vrf == "" {
				vrf = "-"
			}

			svc := intf.ServiceName()
			if svc == "" {
				svc = "-"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				name,
				adminStatus,
				operStatus,
				ipAddr,
				vrf,
				svc,
			)
		}
		w.Flush()

		return nil
	},
}

var interfaceShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show interface details",
	Long: `Show detailed information about an interface.

Requires both -d (device) and -i (interface) flags.

Examples:
  newtron -d leaf1-ny -i Ethernet0 interface show`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		fmt.Printf("Interface: %s\n", bold(interfaceName))

		// Show status with color coding
		adminStatus := intf.AdminStatus()
		operStatus := intf.OperStatus()

		if adminStatus == "up" {
			fmt.Printf("Admin Status: %s\n", green("up"))
		} else if adminStatus != "" {
			fmt.Printf("Admin Status: %s\n", red(adminStatus))
		} else {
			fmt.Printf("Admin Status: -\n")
		}

		if operStatus == "up" {
			fmt.Printf("Oper Status: %s\n", green("up"))
		} else if operStatus != "" {
			fmt.Printf("Oper Status: %s\n", red(operStatus))
		} else {
			fmt.Printf("Oper Status: -\n")
		}

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

		if intf.IsLAGMember() {
			fmt.Printf("\nLAG Member of: %s\n", intf.LAGParent())
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

func init() {
	interfaceCmd.AddCommand(interfaceListCmd)
	interfaceCmd.AddCommand(interfaceShowCmd)
}
