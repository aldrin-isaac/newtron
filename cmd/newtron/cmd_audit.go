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
	Long: `View a network's audit log of configuration changes.

Audit is per-network and server-owned: each network's mutations are
recorded in its own log. list/show read it through the server (the
network is selected by -N / NEWTRON_NETWORK_ID); verify checks the
network's hash chain, or a copied log file offline.

Each entry records timestamp, user, device, operation, and outcome.

Examples:
  newtron -N leaf-fabric audit list --last 24h
  newtron -N leaf-fabric audit list --user alice
  newtron -N leaf-fabric audit list --device leaf1-ny --failures
  newtron -N leaf-fabric audit verify        # server-side, this network's chain
  newtron audit verify ./audit-copy.log      # offline, a copied file`,
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

		// Audit is per-network and server-owned (the log lives in the
		// network's folder). Read through the server's per-network endpoint,
		// which applies the audit.read gate. The audit subcommand skips
		// PersistentPreRunE, so build the client here; the network comes from
		// -N / NEWTRON_NETWORK_ID.
		if err := app.initClient(); err != nil {
			return err
		}
		page, err := app.client.AuditEvents(filter)
		if err != nil {
			return fmt.Errorf("querying audit log: %w", err)
		}
		events := page.Events

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
		// Read one event through the server's per-network detail endpoint
		// (the network comes from -N / NEWTRON_NETWORK_ID). The audit
		// subcommand skips PersistentPreRunE, so build the client here.
		if err := app.initClient(); err != nil {
			return err
		}
		event, err := app.client.AuditEvent(args[0])
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

// auditVerifyCmd verifies a per-network audit hash chain (auth-design.md
// L6). With no argument it verifies the -N network's chain via the server's
// integrity endpoint; with a path it verifies a copied log offline —
// independent of the server that wrote it. The operator runs it periodically
// (cron or after a suspected intrusion) to detect entries that were inserted,
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

Exit 0 = chain clean. Exit 1 = tamper detected; the breakpoint is
printed to stderr. Exit 2 = I/O or argument error.

With no argument, verifies the -N network's chain via the server's
integrity endpoint (audit is per-network, server-owned). Pass a file
path to verify a copied log offline — independent of the server that
wrote it, the trustworthy check after a suspected intrusion.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Explicit path → offline verification of a local file, no server.
		// This is the forensic path: verify a log without trusting the
		// process that produced it.
		if len(args) == 1 {
			result, err := audit.Verify(args[0])
			if err != nil {
				return fmt.Errorf("verifying %s: %w", args[0], err)
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
		}

		// No path → verify this network's chain via the server (the network
		// comes from -N / NEWTRON_NETWORK_ID). The audit subcommand skips
		// PersistentPreRunE, so build the client here.
		if err := app.initClient(); err != nil {
			return err
		}
		res, err := app.client.AuditIntegrity()
		if err != nil {
			return fmt.Errorf("verifying audit integrity: %w", err)
		}
		if res.BreakAt > 0 {
			fmt.Fprintf(os.Stderr, "audit chain broken at entry %d: %s\n", res.BreakAt, res.BreakReason)
			os.Exit(1)
		}
		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(res)
		}
		fmt.Printf("verified %d entries; chain head = %s\n", res.EntryCount, res.ChainHeadHash)
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

	// --json on list/show/verify. Must run after AddCommand so
	// addOutputFlags sees the subcommands and registers --json as a
	// persistent flag the subcommands inherit.
	addOutputFlags(auditCmd)
}
