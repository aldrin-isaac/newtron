package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
)

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "Query routing tables on a device",
	Long: `Look up routes in APP_DB and ASIC_DB on a SONiC device.

APP_DB routes are programmed by FRR via fpmsyncd. ASIC_DB routes are the
SAI representation programmed into the forwarding ASIC by orchagent.
Comparing the two is useful for diagnosing forwarding plane issues.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 route get default 10.0.0.0/24
  newtron -D leaf1 route get Vrf_CUST1 192.168.1.0/24
  newtron -D leaf1 route get-asic 10.0.0.0/24`,
}

var routeGetCmd = &cobra.Command{
	Use:   "get <vrf> <prefix>",
	Short: "Look up a route in APP_DB",
	Long: `Look up a route in APP_DB (FRR → fpmsyncd layer).

APP_DB is Redis database 0. Routes here come from FRR and are the input
to orchagent for ASIC programming. Use this to verify that FRR has learned
and programmed a route.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 route get default 10.0.0.0/24
  newtron -D leaf1 route get Vrf_CUST1 192.168.1.0/24 --json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		vrf, prefix := args[0], args[1]

		entry, err := app.client.GetRoute(app.deviceName, vrf, prefix)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(entry)
		}

		printRouteEntry(entry)
		return nil
	},
}

var routeGetAsicCmd = &cobra.Command{
	Use:   "get-asic <prefix>",
	Short: "Look up a route in ASIC_DB",
	Long: `Look up a route in ASIC_DB (orchagent → SAI layer).

ASIC_DB is Redis database 1. Routes here have been programmed into the ASIC
by orchagent via SAI. A route present in APP_DB but absent from ASIC_DB
indicates an orchagent or SAI programming failure.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 route get-asic 10.0.0.0/24
  newtron -D leaf1 route get-asic 192.168.1.0/24 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		prefix := args[0]

		entry, err := app.client.GetRouteASIC(app.deviceName, prefix)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(entry)
		}

		printRouteEntry(entry)
		return nil
	},
}

// printRouteEntry formats a RouteEntry for human-readable output.
func printRouteEntry(entry *newtron.RouteEntry) {
	fmt.Printf("Prefix:   %s\n", bold(entry.Prefix))
	fmt.Printf("VRF:      %s\n", dash(entry.VRF))
	fmt.Printf("Protocol: %s\n", dash(entry.Protocol))
	fmt.Printf("Source:   %s\n", dash(entry.Source))

	if len(entry.NextHops) == 0 {
		fmt.Println("Next Hops: (none)")
		return
	}

	fmt.Printf("Next Hops (%d):\n", len(entry.NextHops))
	for _, nh := range entry.NextHops {
		addr := dash(nh.Address)
		iface := dash(nh.Interface)
		fmt.Printf("  %-20s via %s\n", addr, iface)
	}
}

func init() {
	routeCmd.AddCommand(routeGetCmd)
	routeCmd.AddCommand(routeGetAsicCmd)
}
