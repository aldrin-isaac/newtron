package main

import (
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

Compares the device's projection (expected state from intent replay) against
the live CONFIG_DB for drift, then checks BGP session state and interface
oper-up status.

Requires -D (device) flag.

Examples:
  newtron -D switch1 health check
  newtron -D switch1 health check --json`,
}

var healthCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Run health checks on the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		report, err := app.client.HealthCheck(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(report)
		}

		fmt.Printf("\nHealth Report for %s\n\n", bold(app.deviceName))

		// Config drift section
		cc := report.ConfigCheck
		if cc != nil {
			driftStatus := "pass"
			if cc.DriftCount > 0 {
				driftStatus = "fail"
			}
			fmt.Printf("Config Drift: %s (%d drift entries)\n",
				formatHealthStatus(driftStatus), cc.DriftCount)
			if cc.DriftCount > 0 {
				t := cli.NewTable("TABLE", "KEY", "TYPE")
				limit := 20
				if len(cc.Entries) < limit {
					limit = len(cc.Entries)
				}
				for _, de := range cc.Entries[:limit] {
					t.Row(de.Table, de.Key, de.Type)
				}
				t.Flush()
				if len(cc.Entries) > 20 {
					fmt.Printf("  ... and %d more drift entries\n", len(cc.Entries)-20)
				}
			}
		}

		// Operational checks section
		fmt.Println()
		t := cli.NewTable("CHECK", "STATUS", "MESSAGE")
		for _, oc := range report.OperChecks {
			t.Row(oc.Check, formatHealthStatus(oc.Status), oc.Message)
		}
		t.Flush()

		fmt.Printf("\nOverall: %s\n", formatHealthStatus(report.Status))

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
	healthCmd.AddCommand(healthCheckCmd)
}
