package node

import (
	"context"
	"fmt"
)

// spec_divergence.go — the read half of the three-way intent comparison
// (#486 rung 0a). It answers a single-device question (§14 — observe, return
// data, not a verdict): how does the intent this device actually has (what was
// applied — its NEWTRON_INTENT records) differ from what the *current* specs
// would apply (the reconstruction)?
//
// Drift (Node.Drift) compares the current-spec projection against the device's
// CONFIG_DB — it fires when the device or the spec moves, and cannot tell which.
// SpecDivergence isolates the spec-moved axis: a non-empty result means the
// specs changed since this device was last provisioned or reconciled, so the
// device is "behind" its specs. Empty means the device's applied intent still
// matches what the specs say today.
//
// Correctness rests on TestOpRoundTrip: export→replay→intent-DB is exactly
// equal for unchanged specs (deterministic, device-independent), so any
// difference here is attributable only to a spec change — no false positives.

// intentMetaFields are the intent envelope, not spec-derived params: they are
// excluded from the field-level divergence report. A change in operation or
// DAG links is a different kind of event than a spec value moving.
var intentMetaFields = map[string]bool{
	"operation": true, "state": true, "_parents": true, "_children": true,
}

// FieldValueChange is one intent param whose resolved value differs between what
// was applied and what current specs would apply. An empty Applied means the
// param is new under the current spec; an empty Current means the current spec
// no longer produces it.
type FieldValueChange struct {
	Applied string
	Current string
}

// ResourceDivergence records how one intent resource has diverged from current
// specs. Orphaned means the resource's defining spec no longer resolves (it was
// deleted or renamed), so reconstruction skips it (§20) — the device is behind
// by a teardown. Otherwise Changes names the params whose resolved value moved.
type ResourceDivergence struct {
	Orphaned bool
	Changes  map[string]FieldValueChange
}

// normalizedIntentDB returns the node's in-memory intent DB in canonical form
// (NormalizeIntentFields per record). Both sides of a spec-divergence diff pass
// through this so link-ordering differences never register as spurious change —
// the same equality semantics TestOpRoundTrip enforces.
func normalizedIntentDB(n *Node) map[string]map[string]string {
	out := make(map[string]map[string]string, len(n.configDB.NewtronIntent))
	for res, fields := range n.configDB.NewtronIntent {
		out[res] = NormalizeIntentFields(fields)
	}
	return out
}

// diffIntents compares applied (#1 — what the device carries) against reresolved
// (#3 — what current specs would apply), both already normalized. It reports one
// ResourceDivergence per diverged resource: orphaned when the resource is gone
// from #3 (its spec no longer resolves), otherwise the set of param changes.
//
// Resources present only in #3 are not reported: replay derives #3 from #1's
// operations, and side-effect intents re-created during replay (deploy-service)
// appear in both, so a #3-only resource is not a spec-evolution class here.
func diffIntents(applied, reresolved map[string]map[string]string) map[string]ResourceDivergence {
	out := make(map[string]ResourceDivergence)
	for res, af := range applied {
		rf, ok := reresolved[res]
		if !ok {
			out[res] = ResourceDivergence{Orphaned: true}
			continue
		}
		changes := make(map[string]FieldValueChange)
		for k, av := range af {
			if intentMetaFields[k] {
				continue
			}
			if cv, ok := rf[k]; !ok {
				changes[k] = FieldValueChange{Applied: av, Current: ""}
			} else if cv != av {
				changes[k] = FieldValueChange{Applied: av, Current: cv}
			}
		}
		for k, cv := range rf {
			if intentMetaFields[k] {
				continue
			}
			if _, ok := af[k]; !ok {
				changes[k] = FieldValueChange{Applied: "", Current: cv}
			}
		}
		if len(changes) > 0 {
			out[res] = ResourceDivergence{Changes: changes}
		}
	}
	return out
}

// SpecDivergence reports, per intent resource, how the device's applied intent
// differs from what the current specs would apply (#486 rung 0a). It is a
// diagnostic read: it re-resolves against current specs on a snapshotted intent
// DB and restores the node's state, mutating nothing on the device.
//
// Mechanism: #1 is the device's own NEWTRON_INTENT records (IntentSnapshot,
// the authority); #3 is those same intents replayed through the current specs
// (RebuildProjectionFromIntents re-resolves caller params against today's spec
// definitions). Both are normalized; the diff is the spec-evolution.
func (n *Node) SpecDivergence(ctx context.Context) (map[string]ResourceDivergence, error) {
	// #1 — what was applied: the device's own intent records. IntentSnapshot
	// reads the device fresh when connected (the api layer connects via
	// connectAndRead before this call) and falls back to the in-memory intent
	// DB offline — no forced connect here, matching ProjectionDiff.
	applied, err := n.IntentSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading applied intents: %w", err)
	}

	// #3 — what current specs would apply: replay #1 through the current specs.
	// Snapshot/restore the live intent DB (the ServiceProjection diagnostic
	// pattern) so this read mutates nothing.
	snapshot := n.SnapshotIntentDB()
	if err := n.RebuildProjectionFromIntents(ctx, applied); err != nil {
		n.RestoreIntentDB(snapshot)
		_ = n.RebuildProjectionFromIntents(ctx, snapshot)
		return nil, fmt.Errorf("re-resolving intents against current specs: %w", err)
	}
	reresolved := normalizedIntentDB(n)

	n.RestoreIntentDB(snapshot)
	if err := n.RebuildProjectionFromIntents(ctx, snapshot); err != nil {
		return nil, fmt.Errorf("restoring projection after spec-divergence read: %w", err)
	}

	return diffIntents(applied, reresolved), nil
}
