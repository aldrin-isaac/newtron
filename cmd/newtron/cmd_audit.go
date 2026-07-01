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
	auditOrder    string
)

var auditListCmd = &cobra.Command{
	Use:   "list",
	Short: "List audit events",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch auditOrder {
		case "", "asc", "desc":
		default:
			return fmt.Errorf("invalid --order %q: expected asc or desc", auditOrder)
		}
		filter := newtron.AuditFilter{
			Device:      auditDevice,
			User:        auditUser,
			Limit:       auditLimit,
			FailureOnly: auditFailures,
			Order:       auditOrder,
		}

		// Parse --last duration
		if auditLast != "" {
			duration, err := time.ParseDuration(auditLast)
			if err != nil {
				return fmt.Errorf("invalid duration: %s", auditLast)
			}
			filter.StartTime = time.Now().Add(-duration)
		}

		// The audit subcommand skips PersistentPreRunE (no network
		// registration needed), so app.settings is nil here. Load
		// settings explicitly to find the audit log path.
		settings, err := newtron.LoadSettings()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}
		auditPath := settings.GetAuditLogPath("")
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

// auditShowCmd prints the full detail of a single audit event by its
// hash-chain ID — the redacted request body and the change-set the operation
// produced, which `audit list` omits to stay scannable. The CLI counterpart of
// GET …/audit/events/{id}.
var auditShowCmd = &cobra.Command{
	Use:   "show <event-id>",
	Short: "Show full detail of a single audit event",
	Long: `Print the full recorded detail of one audit event: the request body
the caller submitted (with secrets redacted) and the CONFIG_DB / intent
change-set the operation produced. 'audit list' shows the envelope; this
shows the content.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// The audit subcommand skips PersistentPreRunE, so resolve the
		// log path from settings explicitly (same as list/verify).
		settings, err := newtron.LoadSettings()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}
		event, err := newtron.FindAuditEvent(settings.GetAuditLogPath(""), args[0])
		if err != nil {
			return fmt.Errorf("finding audit event: %w", err)
		}
		// Detail is inherently structured (nested body + changes); JSON is
		// the honest rendering, so print it regardless of --json.
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(event)
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
	Long: `Walk a JSON-lines audit log written with --audit-integrity
and confirm each entry's PrevHash matches the running chain head and
each entry's ID reproduces SHA256(prev_hash || canonical_json).

Exit 0 = chain clean. Exit 1 = tamper detected; line number printed
to stderr. Exit 2 = I/O or argument error.

If no path is provided, the configured audit log path is used.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// The audit subcommand skips PersistentPreRunE (no network
		// registration needed), so app.settings is nil here.
		// Resolve path explicitly: explicit arg wins, else fall back
		// to the operator's settings file via LoadUserSettings.
		var path string
		switch {
		case len(args) == 1:
			path = args[0]
		default:
			settings, err := newtron.LoadSettings()
			if err != nil {
				return fmt.Errorf("no log path provided and loading settings failed: %w", err)
			}
			path = settings.GetAuditLogPath("")
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
	auditListCmd.Flags().StringVar(&auditOrder, "order", "desc", "Event order: desc (newest first, default) or asc (oldest first)")

	auditCmd.AddCommand(auditListCmd, auditShowCmd, auditVerifyCmd)
}
