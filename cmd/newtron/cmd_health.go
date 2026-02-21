package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron/network"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Health check operations",
	Long: `Run intent-based health checks on SONiC devices.

Requires a loaded topology. Compares live CONFIG_DB against the topology-derived
expected config (same entries the provisioner would write), then checks BGP
session state and interface oper-up status.

Requires -d (device) flag.

Examples:
  newtron -d switch1 health check
  newtron -d switch1 health check --json`,
}

var healthCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Run intent-based health checks on the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		if app.deviceName == "" {
			return fmt.Errorf("device required: use -d <device> flag")
		}
		if !app.net.HasTopology() {
			return fmt.Errorf("no topology loaded — health checks require a topology to define expected state")
		}

		ctx := context.Background()

		provisioner, err := network.NewTopologyProvisioner(app.net)
		if err != nil {
			return fmt.Errorf("creating provisioner: %w", err)
		}

		// Connect the device (VerifyDeviceHealth needs it connected)
		dev, err := app.net.ConnectNode(ctx, app.deviceName)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		report, err := provisioner.VerifyDeviceHealth(ctx, app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(report)
		}

		fmt.Printf("\nHealth Report for %s\n\n", bold(app.deviceName))

		// Config intent section
		cc := report.ConfigCheck
		total := cc.Passed + cc.Failed
		fmt.Printf("Config Intent: %s (%d/%d entries match)\n",
			formatHealthStatus(configStatus(cc.Failed)), cc.Passed, total)
		if cc.Failed > 0 {
			t := cli.NewTable("TABLE", "KEY", "FIELD", "EXPECTED", "ACTUAL")
			limit := 20
			if len(cc.Errors) < limit {
				limit = len(cc.Errors)
			}
			for _, ve := range cc.Errors[:limit] {
				t.Row(ve.Table, ve.Key, ve.Field, ve.Expected, ve.Actual)
			}
			t.Flush()
			if len(cc.Errors) > 20 {
				fmt.Printf("  ... and %d more errors\n", len(cc.Errors)-20)
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

func configStatus(failed int) string {
	if failed == 0 {
		return "pass"
	}
	return "fail"
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
