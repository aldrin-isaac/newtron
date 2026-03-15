package newtron

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/network"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/util"
)

// Node wraps a *node.Node with pending change management.
//
// Each ops method delegates to the internal node.Node, captures the returned
// *node.ChangeSet, appends it to n.pending, and returns only an error.
// Commit() applies all pending changesets, verifies, and moves them to history.
// Execute() is the one-shot pattern: lock → fn → commit → save → unlock.
type Node struct {
	net      *Network
	internal *node.Node
	abstract bool // true when this is an abstract (offline) node

	// pending collects ChangeSets produced by Interface write operations.
	// Accumulated via appendPending; applied and cleared by Commit.
	pending []*node.ChangeSet

	// history holds applied (committed) ChangeSets for VerifyCommitted.
	history []*node.ChangeSet

	// bypassZombieCheck is set by rollback/clear operations to skip the
	// zombie-operation guard in Execute(). These operations ARE the
	// resolution path for zombie operations.
	bypassZombieCheck bool

	// skipHistory is set for rollback operations (both zombie and history)
	// so that rollback commits don't enter the history buffer. Rollback
	// is consumption of history, not new history.
	skipHistory bool
}

// ============================================================================
// Lifecycle methods
// ============================================================================

// Name returns the device name.
func (n *Node) Name() string { return n.internal.Name() }

// IsAbstract returns true if this is an abstract (offline) node.
func (n *Node) IsAbstract() bool { return n.abstract }

// Lock acquires a distributed lock for configuration changes.
func (n *Node) Lock() error { return n.internal.Lock() }

// Unlock releases the distributed lock.
func (n *Node) Unlock() error { return n.internal.Unlock() }

// Save persists the device's running CONFIG_DB to disk.
func (n *Node) Save(ctx context.Context) error { return n.internal.SaveConfig(ctx) }

// Close disconnects from the device. No-op for abstract nodes.
func (n *Node) Close() error {
	if n.abstract {
		return nil
	}
	return n.internal.Disconnect()
}

// Refresh reloads CONFIG_DB from Redis and rebuilds the interface list.
// The ctx parameter is accepted for API consistency but not forwarded
// (node.Node.Refresh does not take a context).
func (n *Node) Refresh(ctx context.Context) error { return n.internal.Refresh() }

// RefreshWithRetry polls Refresh until CONFIG_DB is available or timeout expires.
func (n *Node) RefreshWithRetry(ctx context.Context, timeout time.Duration) error {
	return n.internal.RefreshWithRetry(ctx, timeout)
}

// Interface returns a wrapped Interface for the given interface name.
func (n *Node) Interface(name string) (*Interface, error) {
	intf, err := n.internal.GetInterface(name)
	if err != nil {
		return nil, err
	}
	return &Interface{node: n, internal: intf}, nil
}

// ListInterfaces returns all interface names on the device.
func (n *Node) ListInterfaces() []string { return n.internal.ListInterfaces() }

// InterfaceExists checks if an interface exists on the device.
func (n *Node) InterfaceExists(name string) bool { return n.internal.InterfaceExists(name) }

// LoopbackIP returns the device's loopback IP address.
func (n *Node) LoopbackIP() string { return n.internal.LoopbackIP() }

// HasConfigDB returns true if the CONFIG_DB has been loaded.
func (n *Node) HasConfigDB() bool { return n.internal.ConfigDB() != nil }

// QueryConfigDB reads a CONFIG_DB entry by table and key.
// Returns an empty map (not error) if the entry does not exist.
func (n *Node) QueryConfigDB(table, key string) (map[string]string, error) {
	client := n.internal.ConfigDBClient()
	if client == nil {
		return nil, fmt.Errorf("no CONFIG_DB client for device %s", n.internal.Name())
	}
	return client.Get(table, key)
}

// ConfigDBTableKeys returns all keys in a CONFIG_DB table.
func (n *Node) ConfigDBTableKeys(table string) ([]string, error) {
	client := n.internal.ConfigDBClient()
	if client == nil {
		return nil, fmt.Errorf("no CONFIG_DB client for device %s", n.internal.Name())
	}
	return client.TableKeys(table)
}

// ConfigDBEntryExists returns true if a CONFIG_DB entry exists.
func (n *Node) ConfigDBEntryExists(table, key string) (bool, error) {
	client := n.internal.ConfigDBClient()
	if client == nil {
		return false, fmt.Errorf("no CONFIG_DB client for device %s", n.internal.Name())
	}
	return client.Exists(table, key)
}

// QueryStateDB reads a STATE_DB entry by table and key.
// Returns nil (not error) if the entry does not exist.
func (n *Node) QueryStateDB(table, key string) (map[string]string, error) {
	client := n.internal.StateDBClient()
	if client == nil {
		return nil, fmt.Errorf("no STATE_DB client for device %s", n.internal.Name())
	}
	return client.GetEntry(table, key)
}

// ============================================================================
// Pending change management
// ============================================================================

// appendPending adds a non-nil ChangeSet to the Node's pending list.
// Called by all write methods after each successful operation.
func (n *Node) appendPending(cs *node.ChangeSet) {
	if cs != nil {
		n.pending = append(n.pending, cs)
	}
}

// PendingPreview returns a formatted preview of all pending changes.
func (n *Node) PendingPreview() string {
	var sb strings.Builder
	for _, cs := range n.pending {
		sb.WriteString(cs.Preview())
	}
	return sb.String()
}

// PendingCount returns the total number of pending changes.
func (n *Node) PendingCount() int {
	count := 0
	for _, cs := range n.pending {
		count += len(cs.Changes)
	}
	return count
}

// Commit applies all pending changesets, verifies them, and moves to history.
//
// In abstract mode the shadow ConfigDB was already updated during ops, so
// Commit just records the pending list to history without re-applying.
func (n *Node) Commit(ctx context.Context) (*WriteResult, error) {
	if n.abstract {
		// Abstract mode: shadow already updated during ops
		result := &WriteResult{}
		for _, cs := range n.pending {
			result.Preview += cs.Preview()
			result.ChangeCount += len(cs.Changes)
		}
		result.Applied = true
		result.Verified = true
		n.history = append(n.history, n.pending...)
		n.pending = nil
		return result, nil
	}

	if len(n.pending) == 0 {
		return &WriteResult{}, nil
	}

	result := &WriteResult{}
	for _, cs := range n.pending {
		result.Preview += cs.Preview()
		result.ChangeCount += len(cs.Changes)
	}

	// Build and write intent record for crash recovery.
	ops := make([]sonic.IntentOperation, len(n.pending))
	for i, cs := range n.pending {
		ops[i] = sonic.IntentOperation{
			Name:      cs.Operation,
			Params:    cs.OperationParams,
			ReverseOp: cs.ReverseOp,
		}
	}
	intent := &sonic.OperationIntent{
		Holder:     node.BuildLockHolder(),
		Created:    time.Now().UTC(),
		Operations: ops,
	}
	if err := n.internal.WriteIntent(intent); err != nil {
		return result, fmt.Errorf("writing intent: %w", err)
	}

	// Apply all pending changesets with per-operation progress tracking
	for i, cs := range n.pending {
		// Mark operation started
		now := time.Now().UTC()
		intent.Operations[i].Started = &now
		if err := n.internal.UpdateIntentOps(intent); err != nil {
			util.WithDevice(n.internal.Name()).Warnf("updating intent (started): %v", err)
		}

		if err := cs.Apply(n.internal); err != nil {
			// Intent remains for recovery — do NOT delete
			return result, fmt.Errorf("apply failed: %w", err)
		}

		// Mark operation completed
		completed := time.Now().UTC()
		intent.Operations[i].Completed = &completed
		if err := n.internal.UpdateIntentOps(intent); err != nil {
			util.WithDevice(n.internal.Name()).Warnf("updating intent (completed): %v", err)
		}
	}
	result.Applied = true

	// Verify all pending changesets
	allPassed := true
	var vr VerificationResult
	for _, cs := range n.pending {
		if err := cs.Verify(n.internal); err != nil {
			// Intent remains for recovery — do NOT delete
			return result, fmt.Errorf("verify failed: %w", err)
		}
		if cs.Verification != nil {
			vr.Passed += cs.Verification.Passed
			vr.Failed += cs.Verification.Failed
			for _, e := range cs.Verification.Errors {
				vr.Errors = append(vr.Errors, VerificationError{
					Table:    e.Table,
					Key:      e.Key,
					Field:    e.Field,
					Expected: e.Expected,
					Actual:   e.Actual,
				})
			}
			if cs.Verification.Failed > 0 {
				allPassed = false
			}
		}
	}
	result.Verification = &vr
	if !allPassed {
		// Move to history even on partial failure so VerifyCommitted can recheck.
		// Intent remains — verification failure means state may be inconsistent.
		n.history = append(n.history, n.pending...)
		n.pending = nil
		return result, &VerificationFailedError{
			Device: n.internal.Name(),
			Passed: vr.Passed,
			Failed: vr.Failed,
		}
	}
	result.Verified = true

	// Archive to rolling history before deleting intent (unless this is a rollback)
	if !n.skipHistory {
		n.archiveToHistory(intent)
	}

	// Success — delete intent record
	if err := n.internal.DeleteIntent(); err != nil {
		util.WithDevice(n.internal.Name()).Warnf("deleting intent: %v", err)
	}

	n.history = append(n.history, n.pending...)
	n.pending = nil
	return result, nil
}

// Rollback discards all pending changes without applying them.
func (n *Node) Rollback() {
	n.pending = nil
}

// ============================================================================
// Execute (one-shot pattern)
// ============================================================================

// Execute is the one-shot pattern: lock → fn → commit → save → unlock.
//
// If opts.Execute is false (dry-run), returns a preview without applying.
// If opts.NoSave is true, skips config save after commit.
// If a zombie operation is detected during Lock(), returns ErrDeviceZombieOperation
// unless bypassZombieCheck is set (used by rollback/clear operations).
func (n *Node) Execute(ctx context.Context, opts ExecOpts, fn func(ctx context.Context) error) (*WriteResult, error) {
	if err := n.Lock(); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer n.Unlock()

	// Block if zombie operation exists — device is in unknown partial state.
	// Rollback and ClearZombie bypass this check (they ARE the resolution).
	if !n.bypassZombieCheck && n.internal.ZombieOperation() != nil {
		return nil, util.ErrDeviceZombieOperation
	}

	if err := fn(ctx); err != nil {
		return nil, err
	}

	if !opts.Execute {
		// Dry-run: return preview only
		result := &WriteResult{
			Preview:     n.PendingPreview(),
			ChangeCount: n.PendingCount(),
		}
		n.Rollback()
		return result, nil
	}

	result, err := n.Commit(ctx)
	if err != nil {
		return result, err
	}

	if !opts.NoSave {
		if err := n.Save(ctx); err != nil {
			return result, fmt.Errorf("config save failed: %w", err)
		}
		result.Saved = true
	}

	return result, nil
}

// VerifyCommitted re-verifies all committed changesets against live CONFIG_DB.
func (n *Node) VerifyCommitted(ctx context.Context) (*VerificationResult, error) {
	var vr VerificationResult
	for _, cs := range n.history {
		if err := cs.Verify(n.internal); err != nil {
			return nil, fmt.Errorf("verify failed: %w", err)
		}
		if cs.Verification != nil {
			vr.Passed += cs.Verification.Passed
			vr.Failed += cs.Verification.Failed
			for _, e := range cs.Verification.Errors {
				vr.Errors = append(vr.Errors, VerificationError{
					Table:    e.Table,
					Key:      e.Key,
					Field:    e.Field,
					Expected: e.Expected,
					Actual:   e.Actual,
				})
			}
		}
	}
	return &vr, nil
}

// ============================================================================
// Zombie Operation Methods (crash recovery)
// ============================================================================

// ZombieOperation returns the stale intent found during Lock(), or nil.
func (n *Node) ZombieOperation() *OperationIntent {
	z := n.internal.ZombieOperation()
	if z == nil {
		return nil
	}
	return convertIntent(z)
}

// ReadZombie reads the current intent from STATE_DB (live read, no lock required).
// Returns nil if no intent exists.
func (n *Node) ReadZombie(ctx context.Context) (*OperationIntent, error) {
	z, err := n.internal.ReadIntent()
	if err != nil {
		return nil, err
	}
	if z == nil {
		return nil, nil
	}
	return convertIntent(z), nil
}

// ClearZombie deletes the intent record from STATE_DB without reversing.
// Must be called under lock (use within Execute with bypassZombieCheck).
func (n *Node) ClearZombie(ctx context.Context) error {
	return n.internal.ClearZombie()
}

// SetBypassZombieCheck enables or disables the zombie-operation guard bypass.
// Used by rollback/clear endpoints which ARE the resolution path.
func (n *Node) SetBypassZombieCheck(bypass bool) { n.bypassZombieCheck = bypass }

// SetSkipHistory prevents the next Commit from archiving to history.
// Used by rollback operations — rollback is consumption of history, not new history.
func (n *Node) SetSkipHistory(skip bool) { n.skipHistory = skip }

// PreviewRollback returns a preview of what RollbackZombie would do without
// executing any changes. Uses the same skip/done/reverse logic as RollbackZombie
// so dry-run output exactly matches what execute would do.
func PreviewRollback(intent *OperationIntent) string {
	if intent == nil {
		return "No zombie operation.\n"
	}

	var preview strings.Builder
	preview.WriteString(fmt.Sprintf("Zombie operation by %s (created %s)\n",
		intent.Holder, intent.Created.Format(time.RFC3339)))
	if intent.Phase != "" {
		preview.WriteString(fmt.Sprintf("Phase: %s\n", intent.Phase))
	}
	preview.WriteString("Operations (would be reversed in this order):\n")

	step := 0
	for i := len(intent.Operations) - 1; i >= 0; i-- {
		op := intent.Operations[i]
		step++

		if op.Reversed != nil {
			preview.WriteString(fmt.Sprintf("  %d. [DONE] %s (already reversed)\n", step, op.Name))
			continue
		}
		if op.Started == nil {
			preview.WriteString(fmt.Sprintf("  %d. [SKIP] %s (never started)\n", step, op.Name))
			continue
		}
		if op.ReverseOp == "" {
			preview.WriteString(fmt.Sprintf("  %d. [SKIP] %s (terminal)\n", step, op.Name))
			continue
		}

		status := "partial"
		if op.Completed != nil {
			status = "complete"
		}
		preview.WriteString(fmt.Sprintf("  %d. [REVERSE] %s → %s (%s)\n", step, op.Name, op.ReverseOp, status))
	}

	return preview.String()
}

// RollbackZombie reverses a zombie operation's changes to restore the device
// to its last known-good state. Calls the reverse of each operation in reverse
// order. Must be called within Execute (needs lock). Sets bypassZombieCheck.
//
// Rollback is idempotent: each successfully reversed operation is marked with
// a Reversed timestamp in the intent record. If rollback crashes, retry reads
// the intent, skips already-reversed operations, and continues where it left off.
// Precondition failures (resource doesn't exist) are treated as no-ops — the
// resource was never created or was already cleaned up.
func (n *Node) RollbackZombie(ctx context.Context) (*WriteResult, error) {
	// Read the intent from STATE_DB (live read, authoritative)
	intent, err := n.internal.ReadIntent()
	if err != nil {
		return nil, fmt.Errorf("reading intent: %w", err)
	}
	if intent == nil {
		return &WriteResult{}, nil
	}

	// Transition to rolling_back phase (idempotent — may already be rolling_back
	// from a crashed previous attempt)
	if intent.Phase != sonic.IntentPhaseRollingBack {
		now := time.Now().UTC()
		intent.Phase = sonic.IntentPhaseRollingBack
		intent.RollbackHolder = node.BuildLockHolder()
		intent.RollbackStarted = &now
		if err := n.internal.UpdateIntentOps(intent); err != nil {
			util.WithDevice(n.internal.Name()).Warnf("updating intent phase: %v", err)
		}
	}

	// Reverse each operation in reverse order
	var preview strings.Builder
	reversed := 0
	for i := len(intent.Operations) - 1; i >= 0; i-- {
		op := &intent.Operations[i]

		// Skip operations already reversed (previous rollback attempt)
		if op.Reversed != nil {
			preview.WriteString(fmt.Sprintf("  [DONE] %s (already reversed)\n", op.Name))
			continue
		}

		// Skip operations that never started (no timestamps)
		if op.Started == nil {
			preview.WriteString(fmt.Sprintf("  [SKIP] %s (never started)\n", op.Name))
			continue
		}

		// Skip terminal operations (no ReverseOp — nothing to undo)
		if op.ReverseOp == "" {
			preview.WriteString(fmt.Sprintf("  [SKIP] %s (terminal)\n", op.Name))
			continue
		}

		status := "partial"
		if op.Completed != nil {
			status = "complete"
		}

		cs, err := n.dispatchReverse(ctx, op.ReverseOp, op.Params)
		if errors.Is(err, util.ErrPreconditionFailed) {
			// Resource doesn't exist — nothing to reverse (partial apply
			// that never wrote the resource, or already cleaned up)
			preview.WriteString(fmt.Sprintf("  [SKIP] %s → %s (resource not found)\n", op.Name, op.ReverseOp))
		} else if err != nil {
			return &WriteResult{
				Preview:     preview.String(),
				ChangeCount: reversed,
			}, fmt.Errorf("reversing %s: %w", op.Name, err)
		} else if cs != nil && !cs.IsEmpty() {
			if err := cs.Apply(n.internal); err != nil {
				return &WriteResult{
					Preview:     preview.String(),
					ChangeCount: reversed,
				}, fmt.Errorf("applying reverse of %s: %w", op.Name, err)
			}
			reversed += cs.AppliedCount
			preview.WriteString(fmt.Sprintf("  [REVERSE] %s → %s (%s, %d changes)\n", op.Name, op.ReverseOp, status, cs.AppliedCount))
		} else {
			preview.WriteString(fmt.Sprintf("  [REVERSE] %s → %s (%s, no changes)\n", op.Name, op.ReverseOp, status))
		}

		// Mark this operation as reversed and persist
		now := time.Now().UTC()
		op.Reversed = &now
		if err := n.internal.UpdateIntentOps(intent); err != nil {
			util.WithDevice(n.internal.Name()).Warnf("updating intent (reversed): %v", err)
		}
	}

	// All operations reversed — delete the intent record
	if err := n.internal.ClearZombie(); err != nil {
		return nil, fmt.Errorf("deleting intent after rollback: %w", err)
	}

	return &WriteResult{
		Preview:     preview.String(),
		ChangeCount: reversed,
		Applied:     true,
	}, nil
}

// dispatchReverse calls the internal reverse operation by name and returns
// its ChangeSet. Each reverse operation is existence-checking and
// reference-aware — safe to call on partial state.
func (n *Node) dispatchReverse(ctx context.Context, reverseOp string, params map[string]string) (*node.ChangeSet, error) {
	vlanID := func() int { id, _ := strconv.Atoi(params["vlan_id"]); return id }

	switch reverseOp {
	case "device.delete-vlan":
		return n.internal.DeleteVLAN(ctx, vlanID())
	case "device.remove-vlan-member":
		return n.internal.RemoveVLANMember(ctx, vlanID(), params["interface"])
	case "device.remove-svi":
		return n.internal.RemoveSVI(ctx, vlanID())
	case "device.delete-vrf":
		return n.internal.DeleteVRF(ctx, params["vrf"])
	case "device.unbind-ipvpn":
		return n.internal.UnbindIPVPN(ctx, params["vrf"])
	case "device.remove-bgp-globals":
		return n.internal.RemoveBGPGlobals(ctx)
	case "device.teardown-evpn":
		return n.internal.TeardownEVPN(ctx)
	case "device.unmap-l2vni":
		return n.internal.UnmapL2VNI(ctx, vlanID())
	case "device.delete-acl-table":
		return n.internal.DeleteACLTable(ctx, params["name"])
	case "device.delete-acl-rule":
		return n.internal.DeleteACLRule(ctx, params["table_name"], params["rule_name"])
	case "device.delete-portchannel":
		return n.internal.DeletePortChannel(ctx, params["name"])
	case "device.remove-portchannel-member":
		return n.internal.RemovePortChannelMember(ctx, params["name"], params["member"])
	case "device.remove-static-route":
		return n.internal.RemoveStaticRoute(ctx, params["vrf"], params["prefix"])
	case "device.remove-bgp-neighbor":
		return n.internal.RemoveBGPNeighbor(ctx, params["neighbor_ip"])
	case "device.remove-loopback":
		return n.internal.RemoveLoopback(ctx)
	case "interface.remove-service":
		iface, err := n.internal.GetInterface(params["interface"])
		if err != nil {
			return nil, err
		}
		return iface.RemoveService(ctx)
	case "interface.remove-qos":
		iface, err := n.internal.GetInterface(params["interface"])
		if err != nil {
			return nil, err
		}
		return iface.RemoveQoS(ctx)
	case "interface.unbind-acl":
		iface, err := n.internal.GetInterface(params["interface"])
		if err != nil {
			return nil, err
		}
		return iface.UnbindACL(ctx, params["acl_name"])
	case "interface.unbind-macvpn":
		iface, err := n.internal.GetInterface(params["interface"])
		if err != nil {
			return nil, err
		}
		return iface.UnbindMACVPN(ctx)
	default:
		return nil, fmt.Errorf("unknown reverse operation %q", reverseOp)
	}
}

// convertIntent converts internal sonic.OperationIntent to public OperationIntent.
func convertIntent(z *sonic.OperationIntent) *OperationIntent {
	ops := make([]IntentOperation, len(z.Operations))
	for i, op := range z.Operations {
		ops[i] = IntentOperation{
			Name:      op.Name,
			Params:    op.Params,
			ReverseOp: op.ReverseOp,
			Started:   op.Started,
			Completed: op.Completed,
			Reversed:  op.Reversed,
		}
	}
	return &OperationIntent{
		Holder:          z.Holder,
		Created:         z.Created,
		Phase:           z.Phase,
		RollbackHolder:  z.RollbackHolder,
		RollbackStarted: z.RollbackStarted,
		Operations:      ops,
	}
}

// convertHistoryEntry converts internal sonic.HistoryEntry to public HistoryEntry.
func convertHistoryEntry(h *sonic.HistoryEntry) HistoryEntry {
	ops := make([]IntentOperation, len(h.Operations))
	for i, op := range h.Operations {
		ops[i] = IntentOperation{
			Name:      op.Name,
			Params:    op.Params,
			ReverseOp: op.ReverseOp,
			Started:   op.Started,
			Completed: op.Completed,
			Reversed:  op.Reversed,
		}
	}
	return HistoryEntry{
		Sequence:   h.Sequence,
		Holder:     h.Holder,
		Timestamp:  h.Timestamp,
		Operations: ops,
	}
}

// ============================================================================
// Rolling History Methods
// ============================================================================

// archiveToHistory copies the completed intent's operations to the rolling
// history buffer. Evicts the oldest entries beyond the device's max_history setting.
func (n *Node) archiveToHistory(intent *sonic.OperationIntent) {
	// Read max_history from device settings (falls back to default)
	settings, err := n.internal.ReadSettings()
	if err != nil {
		util.WithDevice(n.internal.Name()).Warnf("reading settings for history: %v", err)
		settings = &sonic.DeviceSettings{MaxHistory: sonic.DefaultMaxHistory}
	}
	if settings.MaxHistory == 0 {
		return // history disabled
	}

	entries, err := n.internal.ReadHistory()
	if err != nil {
		util.WithDevice(n.internal.Name()).Warnf("reading history for archive: %v", err)
		return
	}

	// Compute next sequence number (max existing + 1)
	nextSeq := 1
	for _, e := range entries {
		if e.Sequence >= nextSeq {
			nextSeq = e.Sequence + 1
		}
	}

	entry := &sonic.HistoryEntry{
		Sequence:   nextSeq,
		Holder:     intent.Holder,
		Timestamp:  time.Now().UTC(),
		Operations: intent.Operations,
	}

	if err := n.internal.WriteHistory(entry); err != nil {
		util.WithDevice(n.internal.Name()).Warnf("writing history entry: %v", err)
		return
	}

	// Evict oldest entries beyond max_history
	// entries is sorted newest-first; count includes the new entry
	maxHistory := settings.MaxHistory
	if len(entries)+1 > maxHistory {
		// entries is sorted desc; oldest entries are at the end
		for i := maxHistory - 1; i < len(entries); i++ {
			if err := n.internal.DeleteHistory(entries[i].Sequence); err != nil {
				util.WithDevice(n.internal.Name()).Warnf("evicting history entry %d: %v", entries[i].Sequence, err)
			}
		}
	}
}

// ReadHistory returns the rolling history for this device (newest first).
func (n *Node) ReadHistory(ctx context.Context) (*HistoryResult, error) {
	entries, err := n.internal.ReadHistory()
	if err != nil {
		return nil, err
	}

	result := &HistoryResult{
		Device:  n.internal.Name(),
		Entries: make([]HistoryEntry, len(entries)),
	}
	for i, e := range entries {
		result.Entries[i] = convertHistoryEntry(e)
	}
	return result, nil
}

// RollbackHistory reverses the most recent un-reversed history entry.
// Uses the same dispatchReverse mechanism as zombie rollback.
func (n *Node) RollbackHistory(ctx context.Context) (*WriteResult, error) {
	entries, err := n.internal.ReadHistory()
	if err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}

	// Find the most recent un-reversed entry (entries are sorted newest-first)
	var target *sonic.HistoryEntry
	for _, e := range entries {
		allReversed := true
		for _, op := range e.Operations {
			if op.Reversed == nil {
				allReversed = false
				break
			}
		}
		if !allReversed {
			target = e
			break
		}
	}

	if target == nil {
		return &WriteResult{Preview: "No un-reversed history entries.\n"}, nil
	}

	// Reverse each operation in reverse order (same pattern as RollbackZombie)
	var preview strings.Builder
	reversed := 0
	preview.WriteString(fmt.Sprintf("Rolling back history entry %d (by %s at %s)\n",
		target.Sequence, target.Holder, target.Timestamp.Format(time.RFC3339)))

	for i := len(target.Operations) - 1; i >= 0; i-- {
		op := &target.Operations[i]

		if op.Reversed != nil {
			preview.WriteString(fmt.Sprintf("  [DONE] %s (already reversed)\n", op.Name))
			continue
		}

		if op.Started == nil {
			preview.WriteString(fmt.Sprintf("  [SKIP] %s (never started)\n", op.Name))
			continue
		}

		if op.ReverseOp == "" {
			preview.WriteString(fmt.Sprintf("  [SKIP] %s (terminal)\n", op.Name))
			continue
		}

		status := "partial"
		if op.Completed != nil {
			status = "complete"
		}

		cs, err := n.dispatchReverse(ctx, op.ReverseOp, op.Params)
		if errors.Is(err, util.ErrPreconditionFailed) {
			preview.WriteString(fmt.Sprintf("  [SKIP] %s → %s (resource not found)\n", op.Name, op.ReverseOp))
		} else if err != nil {
			return &WriteResult{
				Preview:     preview.String(),
				ChangeCount: reversed,
			}, fmt.Errorf("reversing %s: %w", op.Name, err)
		} else if cs != nil && !cs.IsEmpty() {
			if err := cs.Apply(n.internal); err != nil {
				return &WriteResult{
					Preview:     preview.String(),
					ChangeCount: reversed,
				}, fmt.Errorf("applying reverse of %s: %w", op.Name, err)
			}
			reversed += cs.AppliedCount
			preview.WriteString(fmt.Sprintf("  [REVERSE] %s → %s (%s, %d changes)\n", op.Name, op.ReverseOp, status, cs.AppliedCount))
		} else {
			preview.WriteString(fmt.Sprintf("  [REVERSE] %s → %s (%s, no changes)\n", op.Name, op.ReverseOp, status))
		}

		// Mark this operation as reversed and persist
		now := time.Now().UTC()
		op.Reversed = &now
		if err := n.internal.UpdateHistory(target); err != nil {
			util.WithDevice(n.internal.Name()).Warnf("updating history (reversed): %v", err)
		}
	}

	return &WriteResult{
		Preview:     preview.String(),
		ChangeCount: reversed,
		Applied:     true,
	}, nil
}

// PreviewRollbackHistory returns a preview of what RollbackHistory would do.
func (n *Node) PreviewRollbackHistory(ctx context.Context) (string, error) {
	entries, err := n.internal.ReadHistory()
	if err != nil {
		return "", fmt.Errorf("reading history: %w", err)
	}

	// Find the most recent un-reversed entry
	var target *sonic.HistoryEntry
	for _, e := range entries {
		allReversed := true
		for _, op := range e.Operations {
			if op.Reversed == nil {
				allReversed = false
				break
			}
		}
		if !allReversed {
			target = e
			break
		}
	}

	if target == nil {
		return "No un-reversed history entries.\n", nil
	}

	var preview strings.Builder
	preview.WriteString(fmt.Sprintf("Would roll back history entry %d (by %s at %s)\n",
		target.Sequence, target.Holder, target.Timestamp.Format(time.RFC3339)))
	preview.WriteString("Operations (would be reversed in this order):\n")

	step := 0
	for i := len(target.Operations) - 1; i >= 0; i-- {
		op := target.Operations[i]
		step++

		if op.Reversed != nil {
			preview.WriteString(fmt.Sprintf("  %d. [DONE] %s (already reversed)\n", step, op.Name))
			continue
		}
		if op.Started == nil {
			preview.WriteString(fmt.Sprintf("  %d. [SKIP] %s (never started)\n", step, op.Name))
			continue
		}
		if op.ReverseOp == "" {
			preview.WriteString(fmt.Sprintf("  %d. [SKIP] %s (terminal)\n", step, op.Name))
			continue
		}

		status := "partial"
		if op.Completed != nil {
			status = "complete"
		}
		preview.WriteString(fmt.Sprintf("  %d. [REVERSE] %s → %s (%s)\n", step, op.Name, op.ReverseOp, status))
	}

	return preview.String(), nil
}

// ============================================================================
// Device Settings
// ============================================================================

// ReadSettings reads newtron operational settings from CONFIG_DB.
func (n *Node) ReadSettings(ctx context.Context) (*DeviceSettings, error) {
	s, err := n.internal.ReadSettings()
	if err != nil {
		return nil, err
	}
	return &DeviceSettings{MaxHistory: s.MaxHistory}, nil
}

// WriteSettings writes newtron operational settings to CONFIG_DB.
func (n *Node) WriteSettings(ctx context.Context, s *DeviceSettings) error {
	return n.internal.WriteSettings(&sonic.DeviceSettings{MaxHistory: s.MaxHistory})
}

// ============================================================================
// Device-level write ops — VLAN
// ============================================================================

// CreateVLAN creates a VLAN on the device.
func (n *Node) CreateVLAN(ctx context.Context, id int, config VLANConfig) error {
	cs, err := n.internal.CreateVLAN(ctx, id, node.VLANConfig{Description: config.Description})
	n.appendPending(cs)
	return err
}

// DeleteVLAN deletes a VLAN from the device.
func (n *Node) DeleteVLAN(ctx context.Context, id int) error {
	cs, err := n.internal.DeleteVLAN(ctx, id)
	n.appendPending(cs)
	return err
}

// AddVLANMember adds an interface to a VLAN.
func (n *Node) AddVLANMember(ctx context.Context, id int, iface string, tagged bool) error {
	cs, err := n.internal.AddVLANMember(ctx, id, iface, tagged)
	n.appendPending(cs)
	return err
}

// RemoveVLANMember removes an interface from a VLAN.
func (n *Node) RemoveVLANMember(ctx context.Context, id int, iface string) error {
	cs, err := n.internal.RemoveVLANMember(ctx, id, iface)
	n.appendPending(cs)
	return err
}

// ConfigureSVI configures the SVI (Layer 3 VLAN interface) for a VLAN.
func (n *Node) ConfigureSVI(ctx context.Context, id int, config SVIConfig) error {
	cs, err := n.internal.ConfigureSVI(ctx, id, node.SVIConfig{
		VRF:        config.VRF,
		IPAddress:  config.IPAddress,
		AnycastMAC: config.AnycastMAC,
	})
	n.appendPending(cs)
	return err
}

// RemoveSVI removes the SVI configuration from a VLAN.
func (n *Node) RemoveSVI(ctx context.Context, id int) error {
	cs, err := n.internal.RemoveSVI(ctx, id)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — VRF
// ============================================================================

// CreateVRF creates a VRF on the device.
func (n *Node) CreateVRF(ctx context.Context, name string, config VRFConfig) error {
	cs, err := n.internal.CreateVRF(ctx, name, node.VRFConfig{})
	n.appendPending(cs)
	return err
}

// DeleteVRF deletes a VRF from the device.
func (n *Node) DeleteVRF(ctx context.Context, name string) error {
	cs, err := n.internal.DeleteVRF(ctx, name)
	n.appendPending(cs)
	return err
}

// AddVRFInterface adds an interface to a VRF.
func (n *Node) AddVRFInterface(ctx context.Context, vrf, iface string) error {
	cs, err := n.internal.AddVRFInterface(ctx, vrf, iface)
	n.appendPending(cs)
	return err
}

// RemoveVRFInterface removes an interface from a VRF.
func (n *Node) RemoveVRFInterface(ctx context.Context, vrf, iface string) error {
	cs, err := n.internal.RemoveVRFInterface(ctx, vrf, iface)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — IPVPN
// ============================================================================

// BindIPVPN binds a VRF to an IP-VPN definition.
// Resolves the IPVPN spec by name from the node's SpecProvider.
func (n *Node) BindIPVPN(ctx context.Context, vrf, ipvpnName string) error {
	ipvpnName = util.NormalizeName(ipvpnName)
	ipvpnDef, err := n.internal.GetIPVPN(ipvpnName)
	if err != nil {
		return fmt.Errorf("ipvpn '%s' not found: %w", ipvpnName, err)
	}
	cs, err := n.internal.BindIPVPN(ctx, vrf, ipvpnDef)
	n.appendPending(cs)
	return err
}

// UnbindIPVPN unbinds the IP-VPN from a VRF.
func (n *Node) UnbindIPVPN(ctx context.Context, vrf string) error {
	cs, err := n.internal.UnbindIPVPN(ctx, vrf)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — BGP
// ============================================================================

// ConfigureBGP configures BGP globals on the device using its profile.
func (n *Node) ConfigureBGP(ctx context.Context) error {
	cs, err := n.internal.ConfigureBGP(ctx)
	n.appendPending(cs)
	return err
}

// RemoveBGPGlobals removes BGP globals from the device.
func (n *Node) RemoveBGPGlobals(ctx context.Context) error {
	cs, err := n.internal.RemoveBGPGlobals(ctx)
	n.appendPending(cs)
	return err
}

// AddBGPNeighbor adds a loopback BGP neighbor (indirect, EVPN overlay).
func (n *Node) AddBGPNeighbor(ctx context.Context, config BGPNeighborConfig) error {
	cs, err := n.internal.AddLoopbackBGPNeighbor(ctx, config.NeighborIP, config.RemoteAS, config.Description, false)
	n.appendPending(cs)
	return err
}

// RemoveBGPNeighbor removes a BGP neighbor by IP.
func (n *Node) RemoveBGPNeighbor(ctx context.Context, ip string) error {
	cs, err := n.internal.RemoveBGPNeighbor(ctx, ip)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF.
func (n *Node) AddStaticRoute(ctx context.Context, vrf, prefix, nexthop string, metric int) error {
	cs, err := n.internal.AddStaticRoute(ctx, vrf, prefix, nexthop, metric)
	n.appendPending(cs)
	return err
}

// RemoveStaticRoute removes a static route from a VRF.
func (n *Node) RemoveStaticRoute(ctx context.Context, vrf, prefix string) error {
	cs, err := n.internal.RemoveStaticRoute(ctx, vrf, prefix)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — EVPN
// ============================================================================

// SetupEVPN configures the full EVPN stack (VTEP + NVO + BGP EVPN).
func (n *Node) SetupEVPN(ctx context.Context, sourceIP string) error {
	cs, err := n.internal.SetupEVPN(ctx, sourceIP)
	n.appendPending(cs)
	return err
}

// TeardownEVPN removes the EVPN configuration from the device.
func (n *Node) TeardownEVPN(ctx context.Context) error {
	cs, err := n.internal.TeardownEVPN(ctx)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — ACL
// ============================================================================

// CreateACLTable creates a new ACL table on the device.
func (n *Node) CreateACLTable(ctx context.Context, name string, config ACLTableConfig) error {
	cs, err := n.internal.CreateACLTable(ctx, name, node.ACLTableConfig{
		Type:        config.Type,
		Stage:       config.Stage,
		Ports:       config.Ports,
		Description: config.Description,
	})
	n.appendPending(cs)
	return err
}

// DeleteACLTable deletes an ACL table and its rules from the device.
func (n *Node) DeleteACLTable(ctx context.Context, name string) error {
	cs, err := n.internal.DeleteACLTable(ctx, name)
	n.appendPending(cs)
	return err
}

// AddACLRule adds a rule to an ACL table.
func (n *Node) AddACLRule(ctx context.Context, acl, ruleName string, config ACLRuleConfig) error {
	cs, err := n.internal.AddACLRule(ctx, acl, ruleName, node.ACLRuleConfig{
		Priority: config.Priority,
		Action:   config.Action,
		SrcIP:    config.SrcIP,
		DstIP:    config.DstIP,
		Protocol: config.Protocol,
		SrcPort:  config.SrcPort,
		DstPort:  config.DstPort,
	})
	n.appendPending(cs)
	return err
}

// RemoveACLRule removes a rule from an ACL table.
func (n *Node) RemoveACLRule(ctx context.Context, acl, ruleName string) error {
	cs, err := n.internal.DeleteACLRule(ctx, acl, ruleName)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — QoS
// ============================================================================

// ApplyQoS applies a QoS policy to an interface.
// Resolves the QoS policy spec by name from the node's SpecProvider.
func (n *Node) ApplyQoS(ctx context.Context, iface, policy string) error {
	policy = util.NormalizeName(policy)
	policyDef, err := n.internal.GetQoSPolicy(policy)
	if err != nil {
		return fmt.Errorf("qos policy '%s' not found: %w", policy, err)
	}
	cs, err := n.internal.ApplyQoS(ctx, iface, policy, policyDef)
	n.appendPending(cs)
	return err
}

// RemoveQoS removes QoS configuration from an interface.
func (n *Node) RemoveQoS(ctx context.Context, iface string) error {
	cs, err := n.internal.RemoveQoS(ctx, iface)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — PortChannel
// ============================================================================

// CreatePortChannel creates a new PortChannel on the device.
func (n *Node) CreatePortChannel(ctx context.Context, name string, config PortChannelConfig) error {
	cs, err := n.internal.CreatePortChannel(ctx, name, node.PortChannelConfig{
		Members:  config.Members,
		MinLinks: config.MinLinks,
		FastRate: config.FastRate,
		Fallback: config.Fallback,
		MTU:      config.MTU,
	})
	n.appendPending(cs)
	return err
}

// DeletePortChannel deletes a PortChannel from the device.
func (n *Node) DeletePortChannel(ctx context.Context, name string) error {
	cs, err := n.internal.DeletePortChannel(ctx, name)
	n.appendPending(cs)
	return err
}

// AddPortChannelMember adds a member interface to a PortChannel.
func (n *Node) AddPortChannelMember(ctx context.Context, pc, member string) error {
	cs, err := n.internal.AddPortChannelMember(ctx, pc, member)
	n.appendPending(cs)
	return err
}

// RemovePortChannelMember removes a member interface from a PortChannel.
func (n *Node) RemovePortChannelMember(ctx context.Context, pc, member string) error {
	cs, err := n.internal.RemovePortChannelMember(ctx, pc, member)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Baseline
// ============================================================================

// ConfigureLoopback configures the loopback interface using the device's profile.
func (n *Node) ConfigureLoopback(ctx context.Context) error {
	cs, err := n.internal.ConfigureLoopback(ctx)
	n.appendPending(cs)
	return err
}

// RemoveLoopback removes the loopback interface configuration.
func (n *Node) RemoveLoopback(ctx context.Context) error {
	cs, err := n.internal.RemoveLoopback(ctx)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Device metadata
// ============================================================================

// SetDeviceMetadata writes fields to DEVICE_METADATA|localhost.
func (n *Node) SetDeviceMetadata(ctx context.Context, fields map[string]string) error {
	cs, err := n.internal.SetDeviceMetadata(ctx, fields)
	n.appendPending(cs)
	return err
}

// Cleanup identifies and removes orphaned configurations on the device.
// cleanupType may be "acls", "vrfs", "vnis", or "" for all.
func (n *Node) Cleanup(ctx context.Context, cleanupType string) (*CleanupSummary, error) {
	cs, summary, err := n.internal.Cleanup(ctx, cleanupType)
	n.appendPending(cs)
	if err != nil || summary == nil {
		return nil, err
	}
	return &CleanupSummary{
		OrphanedACLs:        summary.OrphanedACLs,
		OrphanedVRFs:        summary.OrphanedVRFs,
		OrphanedVNIMappings: summary.OrphanedVNIMappings,
	}, nil
}

// ============================================================================
// Device-level read ops (no changeset, delegation only)
// ============================================================================

// DeviceInfo returns structured device info from the internal node's profile.
func (n *Node) DeviceInfo() (*DeviceInfo, error) {
	p := n.internal.Profile()
	return &DeviceInfo{
		Name:             n.internal.Name(),
		MgmtIP:           p.MgmtIP,
		LoopbackIP:       p.LoopbackIP,
		Platform:         p.Platform,
		Zone:             p.Zone,
		BGPAS:            n.internal.ASNumber(),
		RouterID:         n.internal.RouterID(),
		VTEPSourceIP:     n.internal.VTEPSourceIP(),
		BGPNeighbors:     n.internal.BGPNeighbors(),
		InterfaceCount:   len(n.internal.ListInterfaces()),
		PortChannelCount: len(n.internal.ListPortChannels()),
		VLANCount:        len(n.internal.ListVLANs()),
		VRFCount:         len(n.internal.ListVRFs()),
	}, nil
}

// ListVLANs returns all VLAN IDs on the device.
func (n *Node) ListVLANs() []int { return n.internal.ListVLANs() }

// ListVRFs returns all VRF names on the device.
func (n *Node) ListVRFs() []string { return n.internal.ListVRFs() }

// ListPortChannels returns all PortChannel names on the device.
func (n *Node) ListPortChannels() []string { return n.internal.ListPortChannels() }

// ACLTableExists checks if an ACL table exists on the device.
func (n *Node) ACLTableExists(name string) bool { return n.internal.ACLTableExists(name) }



// VTEPExists checks if a VTEP is configured on the device.
func (n *Node) VTEPExists() bool { return n.internal.VTEPExists() }

// CheckBGPSessions checks that all configured BGP neighbors are Established.
func (n *Node) CheckBGPSessions(ctx context.Context) ([]HealthCheckResult, error) {
	results, err := n.internal.CheckBGPSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]HealthCheckResult, len(results))
	for i, r := range results {
		out[i] = HealthCheckResult{Check: r.Check, Status: r.Status, Message: r.Message}
	}
	return out, nil
}

// GetRoute reads a route from APP_DB for the given VRF and prefix.
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error) {
	re, err := n.internal.GetRoute(ctx, vrf, prefix)
	if err != nil {
		return nil, err
	}
	return convertRouteEntry(re), nil
}

// GetRouteASIC reads a route from ASIC_DB for the given VRF and prefix.
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error) {
	re, err := n.internal.GetRouteASIC(ctx, vrf, prefix)
	if err != nil {
		return nil, err
	}
	return convertRouteEntry(re), nil
}

// GetNeighbor reads a neighbor (ARP/NDP) entry from STATE_DB.
// Returns nil (not error) if the entry does not exist.
func (n *Node) GetNeighbor(ctx context.Context, iface, ip string) (*NeighEntry, error) {
	ne, err := n.internal.GetNeighbor(ctx, iface, ip)
	if err != nil {
		return nil, err
	}
	if ne == nil {
		return nil, nil
	}
	return &NeighEntry{
		IP:        ne.IP,
		Interface: ne.Interface,
		MAC:       ne.MAC,
		Family:    ne.Family,
	}, nil
}

// convertRouteEntry converts a *sonic.RouteEntry to a *RouteEntry.
func convertRouteEntry(re *sonic.RouteEntry) *RouteEntry {
	if re == nil {
		return nil
	}
	entry := &RouteEntry{
		Prefix:   re.Prefix,
		VRF:      re.VRF,
		Protocol: re.Protocol,
		Source:   string(re.Source),
	}
	for _, nh := range re.NextHops {
		entry.NextHops = append(entry.NextHops, RouteNextHop{
			Address:   nh.IP,
			Interface: nh.Interface,
		})
	}
	return entry
}

// ============================================================================
// SSH / device management
// ============================================================================

// ExecCommand executes a command on the device via SSH.
// Returns an error if no SSH tunnel is configured.
func (n *Node) ExecCommand(ctx context.Context, cmd string) (string, error) {
	tunnel := n.internal.Tunnel()
	if tunnel == nil {
		return "", fmt.Errorf("no SSH tunnel configured for device %s", n.internal.Name())
	}
	return tunnel.ExecCommand(cmd)
}

// ConfigReload runs 'config reload -y' on the device via SSH.
func (n *Node) ConfigReload(ctx context.Context) error {
	return n.internal.ConfigReload(ctx)
}

// ApplyFRRDefaults sets FRR runtime defaults not supported by frrcfgd templates.
func (n *Node) ApplyFRRDefaults(ctx context.Context) error {
	return n.internal.ApplyFRRDefaults(ctx)
}

// RestartService restarts a SONiC Docker container by name via SSH.
func (n *Node) RestartService(ctx context.Context, name string) error {
	return n.internal.RestartService(ctx, name)
}

// ============================================================================
// Abstract mode
// ============================================================================

// RegisterPort creates a PORT entry in the shadow ConfigDB.
// Only valid in abstract mode — enables Interface() for the port.
func (n *Node) RegisterPort(name string, fields map[string]string) {
	n.internal.RegisterPort(name, fields)
}

// BuildComposite exports accumulated entries as a CompositeInfo.
// Only valid in abstract mode.
func (n *Node) BuildComposite() *CompositeInfo {
	cc := n.internal.BuildComposite()
	return wrapComposite(cc)
}

// wrapComposite wraps a *node.CompositeConfig into a *CompositeInfo.
func wrapComposite(cc *node.CompositeConfig) *CompositeInfo {
	if cc == nil {
		return nil
	}
	ci := &CompositeInfo{
		DeviceName: cc.Metadata.DeviceName,
		Tables:     make(map[string]int),
		internal:   cc,
	}
	for table, keys := range cc.Tables {
		ci.Tables[table] = len(keys)
		ci.EntryCount += len(keys)
	}
	return ci
}

// ============================================================================
// Composite delivery
// ============================================================================

// DeliverComposite delivers a composite config to the device.
func (n *Node) DeliverComposite(ctx context.Context, ci *CompositeInfo, mode CompositeMode) (*DeliveryResult, error) {
	if ci == nil || ci.internal == nil {
		return nil, fmt.Errorf("nil CompositeInfo or missing internal state")
	}
	cc, ok := ci.internal.(*node.CompositeConfig)
	if !ok {
		return nil, fmt.Errorf("invalid CompositeInfo: unexpected internal type")
	}
	result, err := n.internal.DeliverComposite(cc, node.CompositeMode(mode))
	if err != nil {
		return nil, err
	}
	return &DeliveryResult{
		Applied: result.Applied,
		Skipped: result.Skipped,
		Failed:  result.Failed,
	}, nil
}

// VerifyComposite verifies a composite config against live CONFIG_DB.
func (n *Node) VerifyComposite(ctx context.Context, ci *CompositeInfo) (*VerificationResult, error) {
	if ci == nil || ci.internal == nil {
		return nil, fmt.Errorf("nil CompositeInfo or missing internal state")
	}
	cc, ok := ci.internal.(*node.CompositeConfig)
	if !ok {
		return nil, fmt.Errorf("invalid CompositeInfo: unexpected internal type")
	}
	result, err := n.internal.VerifyComposite(ctx, cc)
	if err != nil {
		return nil, err
	}
	vr := &VerificationResult{
		Passed: result.Passed,
		Failed: result.Failed,
	}
	for _, e := range result.Errors {
		vr.Errors = append(vr.Errors, VerificationError{
			Table:    e.Table,
			Key:      e.Key,
			Field:    e.Field,
			Expected: e.Expected,
			Actual:   e.Actual,
		})
	}
	return vr, nil
}

// ============================================================================
// HealthCheck
// ============================================================================

// HealthCheck runs topology-driven health checks on this device.
// Requires the Network to have a loaded topology.
func (n *Node) HealthCheck(ctx context.Context) (*HealthReport, error) {
	if n.net == nil || !n.net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded — health checks require a topology"}
	}
	provisioner, err := network.NewTopologyProvisioner(n.net.internal)
	if err != nil {
		return nil, err
	}
	report, err := provisioner.VerifyDeviceHealth(ctx, n.internal.Name())
	if err != nil {
		return nil, err
	}
	return convertHealthReport(report), nil
}

// convertHealthReport converts a *network.HealthReport to a *HealthReport.
func convertHealthReport(r *network.HealthReport) *HealthReport {
	hr := &HealthReport{
		Device: r.Device,
		Status: r.Status,
	}
	if r.ConfigCheck != nil {
		hr.ConfigCheck = &VerificationResult{
			Passed: r.ConfigCheck.Passed,
			Failed: r.ConfigCheck.Failed,
		}
		for _, e := range r.ConfigCheck.Errors {
			hr.ConfigCheck.Errors = append(hr.ConfigCheck.Errors, VerificationError{
				Table: e.Table, Key: e.Key, Field: e.Field,
				Expected: e.Expected, Actual: e.Actual,
			})
		}
	}
	for _, oc := range r.OperChecks {
		hr.OperChecks = append(hr.OperChecks, HealthCheckResult{
			Check: oc.Check, Status: oc.Status, Message: oc.Message,
		})
	}
	return hr
}

// ============================================================================
// Status views (read methods)
// ============================================================================

// BGPStatus returns comprehensive BGP status: config + operational state.
func (n *Node) BGPStatus() (*BGPStatusResult, error) {
	resolved := n.internal.Resolved()
	configDB := n.internal.ConfigDB()

	result := &BGPStatusResult{
		LocalAS:    resolved.UnderlayASN,
		RouterID:   resolved.RouterID,
		LoopbackIP: resolved.LoopbackIP,
		EVPNPeers:  resolved.BGPNeighbors,
	}

	if configDB == nil {
		return result, nil
	}

	stateClient := n.internal.StateDBClient()
	for key, neighbor := range configDB.BGPNeighbor {
		parts := strings.SplitN(key, "|", 2)
		var vrf, addr string
		if len(parts) == 2 {
			vrf = parts[0]
			addr = parts[1]
		} else {
			addr = key
		}

		nType := "indirect"
		if neighbor.LocalAddr != "" && neighbor.LocalAddr != resolved.LoopbackIP {
			nType = "direct"
		}

		adminStatus := neighbor.AdminStatus
		if adminStatus == "" {
			adminStatus = "up"
		}

		ns := BGPNeighborStatus{
			Address:   addr,
			VRF:       vrf,
			Type:      nType,
			RemoteAS:  neighbor.ASN,
			LocalAddr: neighbor.LocalAddr,
			Admin:     adminStatus,
			Name:      neighbor.Name,
		}

		// Get operational state from STATE_DB
		if stateClient != nil && vrf != "" {
			entry, err := stateClient.GetBGPNeighborState(vrf, addr)
			if err == nil {
				ns.State = entry.State
				ns.PfxRcvd = entry.PfxRcvd
				ns.PfxSent = entry.PfxSent
				ns.Uptime = entry.Uptime
				if ns.RemoteAS == "" {
					ns.RemoteAS = entry.RemoteAS
				}
			}
		}

		result.Neighbors = append(result.Neighbors, ns)
	}
	return result, nil
}

// EVPNStatus returns comprehensive EVPN status: config + operational state.
func (n *Node) EVPNStatus() (*EVPNStatusResult, error) {
	configDB := n.internal.ConfigDB()

	result := &EVPNStatusResult{
		VTEPs: make(map[string]string),
		NVOs:  make(map[string]string),
	}

	if configDB != nil {
		for name, vtep := range configDB.VXLANTunnel {
			result.VTEPs[name] = vtep.SrcIP
		}
		for name, nvo := range configDB.VXLANEVPNNVO {
			result.NVOs[name] = nvo.SourceVTEP
		}
		result.VNICount = len(configDB.VXLANTunnelMap)

		// VNI mappings
		for _, mapping := range configDB.VXLANTunnelMap {
			resType := "L2"
			res := mapping.VLAN
			if mapping.VRF != "" {
				resType = "L3"
				res = mapping.VRF
			}
			result.VNIMappings = append(result.VNIMappings, VNIMapping{
				VNI:      mapping.VNI,
				Type:     resType,
				Resource: res,
			})
		}

		// VRFs with L3VNI
		for _, vrfName := range n.internal.ListVRFs() {
			vrf, err := n.internal.GetVRF(vrfName)
			if err != nil || vrf.L3VNI <= 0 {
				continue
			}
			result.L3VNIVRFs = append(result.L3VNIVRFs, L3VNIEntry{
				VRF:   vrfName,
				L3VNI: vrf.L3VNI,
			})
		}
	}

	// Operational state from STATE_DB
	stateDB := n.internal.StateDB()
	if stateDB != nil {
		for name, tunnelState := range stateDB.VXLANTunnelTable {
			if configDB != nil {
				if _, isLocal := configDB.VXLANTunnel[name]; isLocal {
					result.VTEPStatus = tunnelState.OperStatus
					continue
				}
			}
			result.RemoteVTEPs = append(result.RemoteVTEPs, name)
		}
	}

	return result, nil
}

// VLANStatus returns all VLANs with summary details.
func (n *Node) VLANStatus() ([]VLANStatusEntry, error) {
	var result []VLANStatusEntry
	for _, id := range n.internal.ListVLANs() {
		vlan, err := n.internal.GetVLAN(id)
		if err != nil {
			continue
		}
		entry := VLANStatusEntry{
			ID:          vlan.ID,
			Name:        vlan.Name,
			L2VNI:       vlan.L2VNI(),
			SVI:         vlan.SVIStatus,
			MemberCount: len(vlan.Members),
			MemberNames: vlan.Members,
		}
		if vlan.MACVPNInfo != nil {
			entry.MACVPN = vlan.MACVPNInfo.Name
			entry.MACVPNInfo = &VLANMACVPNDetail{
				Name:           vlan.MACVPNInfo.Name,
				L2VNI:          vlan.MACVPNInfo.L2VNI,
				ARPSuppression: vlan.MACVPNInfo.ARPSuppression,
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

// ShowVLAN returns VLAN info for a given VLAN ID.
func (n *Node) ShowVLAN(id int) (*VLANStatusEntry, error) {
	vlan, err := n.internal.GetVLAN(id)
	if err != nil {
		return nil, err
	}
	entry := &VLANStatusEntry{
		ID:          vlan.ID,
		Name:        vlan.Name,
		L2VNI:       vlan.L2VNI(),
		SVI:         vlan.SVIStatus,
		MemberCount: len(vlan.Members),
		MemberNames: vlan.Members,
	}
	if vlan.MACVPNInfo != nil {
		entry.MACVPN = vlan.MACVPNInfo.Name
		entry.MACVPNInfo = &VLANMACVPNDetail{
			Name:           vlan.MACVPNInfo.Name,
			L2VNI:          vlan.MACVPNInfo.L2VNI,
			ARPSuppression: vlan.MACVPNInfo.ARPSuppression,
		}
	}
	return entry, nil
}

// VRFStatus returns all VRFs with operational state from STATE_DB.
func (n *Node) VRFStatus() ([]VRFStatusEntry, error) {
	var result []VRFStatusEntry
	for _, name := range n.internal.ListVRFs() {
		vrf, err := n.internal.GetVRF(name)
		if err != nil {
			continue
		}
		entry := VRFStatusEntry{
			Name:       name,
			L3VNI:      vrf.L3VNI,
			Interfaces: len(vrf.Interfaces),
		}
		stateClient := n.internal.StateDBClient()
		if stateClient != nil {
			stateEntry, err := stateClient.GetEntry("VRF_TABLE", name)
			if err == nil && stateEntry != nil {
				entry.State = stateEntry["state"]
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

// ShowVRF returns VRF info including BGP neighbors from CONFIG_DB.
func (n *Node) ShowVRF(name string) (*VRFDetail, error) {
	vrf, err := n.internal.GetVRF(name)
	if err != nil {
		return nil, err
	}
	detail := &VRFDetail{
		Name:       vrf.Name,
		L3VNI:      vrf.L3VNI,
		Interfaces: vrf.Interfaces,
	}

	// Extract BGP neighbors for this VRF from CONFIG_DB
	configDB := n.internal.ConfigDB()
	if configDB != nil {
		vrfPrefix := name + "|"
		for key, neighbor := range configDB.BGPNeighbor {
			if !strings.HasPrefix(key, vrfPrefix) {
				continue
			}
			parts := strings.SplitN(key, "|", 2)
			if len(parts) != 2 {
				continue
			}
			detail.BGPNeighbors = append(detail.BGPNeighbors, BGPNeighborEntry{
				Address:     parts[1],
				ASN:         neighbor.ASN,
				Description: neighbor.Name,
			})
		}
	}
	return detail, nil
}

// LAGStatus returns all PortChannels with operational state.
func (n *Node) LAGStatus() ([]LAGStatusEntry, error) {
	var result []LAGStatusEntry
	for _, pcName := range n.internal.ListPortChannels() {
		pc, err := n.internal.GetPortChannel(pcName)
		if err != nil {
			continue
		}
		entry := LAGStatusEntry{
			Name:          pc.Name,
			AdminStatus:   pc.AdminStatus,
			Members:       pc.Members,
			ActiveMembers: pc.ActiveMembers,
		}
		if intf, err := n.internal.GetInterface(pc.Name); err == nil {
			entry.OperStatus = intf.OperStatus()
			entry.MTU = intf.MTU()
		}
		result = append(result, entry)
	}
	return result, nil
}

// ShowLAGDetail returns LAG info including interface MTU.
func (n *Node) ShowLAGDetail(name string) (*LAGStatusEntry, error) {
	pc, err := n.internal.GetPortChannel(name)
	if err != nil {
		return nil, err
	}
	entry := &LAGStatusEntry{
		Name:          pc.Name,
		AdminStatus:   pc.AdminStatus,
		Members:       pc.Members,
		ActiveMembers: pc.ActiveMembers,
	}
	if intf, err := n.internal.GetInterface(pc.Name); err == nil {
		entry.OperStatus = intf.OperStatus()
		entry.MTU = intf.MTU()
	}
	return entry, nil
}

// ListACLs returns all ACL tables with summary info.
func (n *Node) ListACLs() ([]ACLTableSummary, error) {
	configDB := n.internal.ConfigDB()
	if configDB == nil {
		return nil, nil
	}
	// Count rules per ACL table
	ruleCounts := make(map[string]int, len(configDB.ACLTable))
	for ruleKey := range configDB.ACLRule {
		if i := strings.IndexByte(ruleKey, '|'); i >= 0 {
			ruleCounts[ruleKey[:i]]++
		}
	}
	var result []ACLTableSummary
	for name, table := range configDB.ACLTable {
		result = append(result, ACLTableSummary{
			Name:       name,
			Type:       table.Type,
			Stage:      table.Stage,
			Interfaces: table.Ports,
			RuleCount:  ruleCounts[name],
		})
	}
	return result, nil
}

// ShowACL returns an ACL table with all its rules.
func (n *Node) ShowACL(name string) (*ACLTableDetail, error) {
	configDB := n.internal.ConfigDB()
	if configDB == nil {
		return nil, fmt.Errorf("not connected to device config_db")
	}
	table, ok := configDB.ACLTable[name]
	if !ok {
		return nil, &NotFoundError{Resource: "ACL table", Name: name}
	}
	detail := &ACLTableDetail{
		Name:        name,
		Type:        table.Type,
		Stage:       table.Stage,
		Interfaces:  table.Ports,
		Description: table.PolicyDesc,
	}
	prefix := name + "|"
	for ruleKey, rule := range configDB.ACLRule {
		if !strings.HasPrefix(ruleKey, prefix) {
			continue
		}
		detail.Rules = append(detail.Rules, ACLRuleInfo{
			Name:     strings.TrimPrefix(ruleKey, prefix),
			Priority: rule.Priority,
			Action:   rule.PacketAction,
			SrcIP:    rule.SrcIP,
			DstIP:    rule.DstIP,
			Protocol: rule.IPProtocol,
			SrcPort:  rule.L4SrcPort,
			DstPort:  rule.L4DstPort,
		})
	}
	return detail, nil
}

// GetServiceBinding returns the service name bound to an interface (empty if none).
func (n *Node) GetServiceBinding(iface string) (string, error) {
	intf, err := n.internal.GetInterface(iface)
	if err != nil {
		return "", err
	}
	return intf.ServiceName(), nil
}

// GetServiceBindingDetail returns the full service binding: name, IPs, VRF.
func (n *Node) GetServiceBindingDetail(iface string) (*ServiceBindingDetail, error) {
	intf, err := n.internal.GetInterface(iface)
	if err != nil {
		return nil, err
	}
	return &ServiceBindingDetail{
		Service:     intf.ServiceName(),
		IPAddresses: intf.IPAddresses(),
		VRF:         intf.VRF(),
	}, nil
}

// ListInterfaceDetails returns summary info for all interfaces on the device.
func (n *Node) ListInterfaceDetails() ([]InterfaceSummary, error) {
	var result []InterfaceSummary
	for _, name := range n.internal.ListInterfaces() {
		intf, err := n.internal.GetInterface(name)
		if err != nil {
			continue
		}
		result = append(result, InterfaceSummary{
			Name:        name,
			AdminStatus: intf.AdminStatus(),
			OperStatus:  intf.OperStatus(),
			IPAddresses: intf.IPAddresses(),
			VRF:         intf.VRF(),
			Service:     intf.ServiceName(),
		})
	}
	return result, nil
}

// ShowInterfaceDetail returns all properties of a single interface.
func (n *Node) ShowInterfaceDetail(name string) (*InterfaceDetail, error) {
	intf, err := n.internal.GetInterface(name)
	if err != nil {
		return nil, err
	}
	return &InterfaceDetail{
		Name:        name,
		AdminStatus: intf.AdminStatus(),
		OperStatus:  intf.OperStatus(),
		Speed:       intf.Speed(),
		MTU:         intf.MTU(),
		IPAddresses: intf.IPAddresses(),
		VRF:         intf.VRF(),
		Service:     intf.ServiceName(),
		PCMember:    intf.IsPortChannelMember(),
		PCParent:    intf.PortChannelParent(),
		IngressACL:  intf.IngressACL(),
		EgressACL:   intf.EgressACL(),
		PCMembers:   intf.PortChannelMembers(),
		VLANMembers: intf.VLANMembers(),
	}, nil
}

// GetInterfaceProperty returns a single property of an interface.
func (n *Node) GetInterfaceProperty(name, property string) (string, error) {
	iface, err := n.internal.GetInterface(name)
	if err != nil {
		return "", err
	}
	switch property {
	case "admin_status", "admin-status":
		return iface.AdminStatus(), nil
	case "oper_status", "oper-status":
		return iface.OperStatus(), nil
	case "speed":
		return iface.Speed(), nil
	case "mtu":
		mtu := iface.MTU()
		if mtu == 0 {
			return "", nil
		}
		return fmt.Sprintf("%d", mtu), nil
	case "description":
		return iface.Description(), nil
	case "vrf":
		return iface.VRF(), nil
	case "service":
		return iface.ServiceName(), nil
	case "ip":
		return strings.Join(iface.IPAddresses(), ", "), nil
	default:
		return "", &ValidationError{Field: "property", Message: "unknown property: " + property}
	}
}
