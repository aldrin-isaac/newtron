package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/cli"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
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
		filter := newtron.AuditFilter{
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

		auditPath := app.settings.GetAuditLogPath(app.specDir)
		events, err := newtron.QueryAuditLog(auditPath, filter)
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

		t := cli.NewTable("TIMESTAMP", "USER", "DEVICE", "OPERATION", "STATUS")

		for _, event := range events {
			status := green("ok")
			if !event.Success {
				status = red("failed")
			}
			if event.DryRun {
				status = yellow("dry-run")
			}

			t.Row(
				event.Timestamp,
				event.User,
				event.Device,
				event.Operation,
				status,
			)
		}
		t.Flush()

		return nil
	},
}

// auditVerifyCmd verifies the hash chain on an audit log file
// (auth-design.md L6). The operator runs it periodically (cron or
// after a suspected intrusion) to detect entries that were inserted,
// removed, reordered, or modified after the fact.
//
// Exit codes:
//   0   chain verified clean (or file missing — nothing to tamper)
//   1   tamper detected; the breakpoint is printed to stderr
//   2   I/O or argument error
var auditVerifyCmd = &cobra.Command{
	Use:   "verify [path]",
	Short: "Verify the hash chain on an audit log",
	Long: `Walk a JSON-lines audit log written with --audit-log-integrity
and confirm each entry's PrevHash matches the running chain head and
each entry's ID reproduces SHA256(prev_hash || canonical_json).

Exit 0 = chain clean. Exit 1 = tamper detected; line number printed
to stderr. Exit 2 = I/O or argument error.

If no path is provided, the configured audit log path is used.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := app.settings.GetAuditLogPath(app.specDir)
		if len(args) == 1 {
			path = args[0]
		}
		result, err := audit.Verify(path)
		if err != nil {
			return fmt.Errorf("verifying %s: %w", path, err)
		}
		if result.BrokenAt > 0 {
			fmt.Fprintf(os.Stderr, "audit chain broken at line %d: %s\n", result.BrokenAt, result.Reason)
			os.Exit(1)
		}
		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		fmt.Printf("verified %d entries; chain head = %s\n", result.Entries, result.Head)
		return nil
	},
}

func init() {
	auditListCmd.Flags().StringVar(&auditDevice, "device", "", "Filter by device")
	auditListCmd.Flags().StringVar(&auditUser, "user", "", "Filter by user")
	auditListCmd.Flags().StringVar(&auditLast, "last", "", "Show events from last duration (e.g., 24h, 7d)")
	auditListCmd.Flags().IntVar(&auditLimit, "limit", 100, "Maximum events to show")
	auditListCmd.Flags().BoolVar(&auditFailures, "failures", false, "Show only failed operations")

	auditCmd.AddCommand(auditListCmd, auditVerifyCmd)
}
