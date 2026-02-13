package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/audit"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "View audit logs",
	Long: `View audit logs of configuration changes.

All configuration changes are logged with:
  - Timestamp
  - User who made the change
  - Device affected
  - Operation performed
  - Success/failure status

Examples:
  newtron audit list --device leaf1-ny
  newtron audit list --last 24h
  newtron audit list --user alice`,
}

var (
	auditDevice   string
	auditUser     string
	auditLast     string
	auditLimit    int
	auditFailures bool
)

var auditListCmd = &cobra.Command{
	Use:   "list",
	Short: "List audit events",
	RunE: func(cmd *cobra.Command, args []string) error {
		filter := audit.Filter{
			Device:      auditDevice,
			User:        auditUser,
			Limit:       auditLimit,
			FailureOnly: auditFailures,
		}

		// Parse --last duration
		if auditLast != "" {
			duration, err := time.ParseDuration(auditLast)
			if err != nil {
				return fmt.Errorf("invalid duration: %s", auditLast)
			}
			filter.StartTime = time.Now().Add(-duration)
		}

		events, err := audit.Query(filter)
		if err != nil {
			return fmt.Errorf("querying audit log: %w", err)
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(events)
		}

		if len(events) == 0 {
			fmt.Println("No audit events found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIMESTAMP\tUSER\tDEVICE\tOPERATION\tSTATUS")
		fmt.Fprintln(w, "---------\t----\t------\t---------\t------")

		for _, event := range events {
			status := green("ok")
			if !event.Success {
				status = red("failed")
			}
			if event.DryRun {
				status = yellow("dry-run")
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				event.Timestamp.Format("2006-01-02 15:04:05"),
				event.User,
				event.Device,
				event.Operation,
				status,
			)
		}
		w.Flush()

		return nil
	},
}

func init() {
	auditListCmd.Flags().StringVar(&auditDevice, "device", "", "Filter by device")
	auditListCmd.Flags().StringVar(&auditUser, "user", "", "Filter by user")
	auditListCmd.Flags().StringVar(&auditLast, "last", "", "Show events from last duration (e.g., 24h, 7d)")
	auditListCmd.Flags().IntVar(&auditLimit, "limit", 100, "Maximum events to show")
	auditListCmd.Flags().BoolVar(&auditFailures, "failures", false, "Show only failed operations")

	auditCmd.AddCommand(auditListCmd)
}
