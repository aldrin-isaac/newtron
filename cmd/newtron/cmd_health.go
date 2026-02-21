package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Health check operations",
	Long: `Run health checks on SONiC devices.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny health check
  newtron -d leaf1-ny health check --check bgp`,
}

var healthCheckName string

var healthCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Run health checks on the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		results, err := dev.RunHealthChecks(ctx, healthCheckName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(results)
		}

		fmt.Printf("\nHealth Report for %s\n\n", bold(app.deviceName))

		t := cli.NewTable("CHECK", "STATUS", "MESSAGE")
		for _, r := range results {
			t.Row(r.Check, formatHealthStatus(r.Status), r.Message)
		}
		t.Flush()

		// Overall status
		overall := "pass"
		for _, r := range results {
			if r.Status == "fail" {
				overall = "fail"
				break
			}
			if r.Status == "warn" {
				overall = "warn"
			}
		}
		fmt.Printf("\nOverall: %s\n", formatHealthStatus(overall))

		return nil
	},
}

func formatHealthStatus(status string) string {
	switch status {
	case "pass":
		return green("PASS")
	case "warn":
		return yellow("WARN")
	case "fail":
		return red("FAIL")
	default:
		return status
	}
}

func init() {
	healthCheckCmd.Flags().StringVar(&healthCheckName, "check", "", "Run specific health check (bgp, interfaces, evpn, lag)")

	healthCmd.AddCommand(healthCheckCmd)
}
