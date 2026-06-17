// audit_ops.go — public API surface for reading the audit log and
// its hash-chain integrity status (issue #196).
//
// The audit log itself is operator-configured at server startup
// (`--audit-log <path>`) and written by the audit middleware in
// pkg/newtron/api/. This file exposes two read-side operations
// the HTTP handlers call:
//
//   - QueryAuditEvents — paged, filtered read of audit events
//   - VerifyAuditIntegrity — L6 hash-chain verification
//
// Both honor the same engage-when-configured PermAuditRead gate
// (see CheckAuditReadGate in authorization_ops.go).
//
// Per DPN §27 (single owner): the audit package owns the log file
// and its read/verify operations. These functions are the thin
// HTTP-handler boundary; the CLI's `bin/newtron audit list/verify`
// subcommands reuse the same audit.Filter / audit.Verify primitives
// through pkg/newtron/audit.go's QueryAuditLog.
package newtron

import (
	"fmt"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// QueryAuditEvents reads up to `filter.Limit` audit events from the
// log at path, starting at `filter.Offset`, filtered by the
// remaining Filter fields (device, user, operation, service,
// interface, time range, success/failure). Returns both the page
// of events and a Total count of events matching the filter
// without paging — so a browsing UI can render "N of M entries"
// and decide whether to fetch the next page.
//
// Returns an empty page (Total=0, Events=nil) when the audit log
// file does not exist — this is the "no audit log configured"
// case at the CLI / HTTP boundary; not an error worth surfacing.
//
// path is operator-supplied at server startup (--audit-log on
// cmd/newt-server). Empty path returns an empty page so the
// no-audit-log deployment doesn't 500 on a read.
func QueryAuditEvents(path string, filter AuditFilter) (AuditEventPage, error) {
	if path == "" {
		return AuditEventPage{Events: []AuditEvent{}, Total: 0}, nil
	}
	// First pass: count without paging so the Total field reflects
	// the unbounded filtered cardinality. Cheap on typical log
	// sizes (audit logs are append-only and small relative to the
	// rest of the system). The Limit/Offset are stripped because
	// they govern the page, not the count.
	countFilter := filter
	countFilter.Limit = 0
	countFilter.Offset = 0
	allMatched, err := QueryAuditLog(path, countFilter)
	if err != nil {
		return AuditEventPage{}, fmt.Errorf("counting audit events: %w", err)
	}
	total := len(allMatched)

	// Second pass: the actual page. If the caller passed Limit=0
	// (default), return everything that matched.
	events, err := QueryAuditLog(path, filter)
	if err != nil {
		return AuditEventPage{}, fmt.Errorf("reading audit events: %w", err)
	}

	page := AuditEventPage{
		Events: events,
		Total:  total,
	}
	if filter.Limit > 0 && filter.Offset+filter.Limit < total {
		next := filter.Offset + filter.Limit
		page.NextOffset = &next
	}
	return page, nil
}

// VerifyAuditIntegrity walks the audit log's hash chain end to end
// and returns a structured result describing chain status. Pure
// read; never mutates the log. Cheap for typical log sizes
// (entries are JSON-lines; walking is O(n) in entry count).
//
// path is operator-supplied at server startup. Empty path returns
// a zero-valued result with VerifiedAt set — same convention as
// QueryAuditEvents for the no-audit-log case.
func VerifyAuditIntegrity(path string) (AuditIntegrityResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if path == "" {
		return AuditIntegrityResult{VerifiedAt: now}, nil
	}
	res, err := audit.Verify(path)
	if err != nil {
		return AuditIntegrityResult{}, fmt.Errorf("verifying audit log: %w", err)
	}
	out := AuditIntegrityResult{
		ChainHeadHash: res.Head,
		EntryCount:    res.Entries,
		BreakAt:       res.BrokenAt,
		BreakReason:   res.Reason,
		VerifiedAt:    now,
	}
	return out, nil
}

