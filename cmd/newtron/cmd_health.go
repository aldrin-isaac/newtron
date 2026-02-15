package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/health"
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

		checker := health.NewChecker()

		// Run specific check or all checks
		if healthCheckName != "" {
			result, err := checker.RunCheck(ctx, dev, healthCheckName)
			if err != nil {
				return err
			}

			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(result)
			}

			printHealthResult(*result)
			return nil
		}

		// Run all checks
		report, err := checker.Run(ctx, dev)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(report)
		}

		fmt.Printf("\nHealth Report for %s\n", bold(app.deviceName))
		fmt.Printf("Timestamp: %s\n", report.Timestamp.Format("2006-01-02 15:04:05"))
		fmt.Printf("Duration: %s\n\n", report.Duration)

		t := cli.NewTable("CHECK", "STATUS", "MESSAGE", "DURATION")

		for _, result := range report.Results {
			status := formatStatus(result.Status)
			t.Row(result.Check, status, result.Message, result.Duration.String())
		}
		t.Flush()

		fmt.Printf("\nOverall Status: %s\n", formatStatus(report.Overall))

		return nil
	},
}

func printHealthResult(result health.Result) {
	fmt.Printf("\nHealth Check: %s\n", bold(result.Check))
	fmt.Printf("Status: %s\n", formatStatus(result.Status))
	fmt.Printf("Message: %s\n", result.Message)
	fmt.Printf("Duration: %s\n", result.Duration)

	if result.Details != nil {
		fmt.Printf("Details: %v\n", result.Details)
	}
}

func formatStatus(status health.Status) string {
	switch status {
	case health.StatusOK:
		return green("OK")
	case health.StatusWarning:
		return yellow("WARNING")
	case health.StatusCritical:
		return red("CRITICAL")
	default:
		return string(status)
	}
}

func init() {
	healthCheckCmd.Flags().StringVar(&healthCheckName, "check", "", "Run specific health check (interfaces, lag, bgp, vxlan, evpn)")

	healthCmd.AddCommand(healthCheckCmd)
}
