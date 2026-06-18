package node

import (
	"fmt"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// ============================================================================
// NEWTRON_INTENT — sole owner of intent record writes
// Intent DAG: every record has _parents/_children encoding structural deps
// ============================================================================

// writeIntent creates or idempotently updates a NEWTRON_INTENT record and
// registers with all declared parents. Returns error if:
//   - Any parent does not exist (I4)
//   - Intent exists at resource with different parents (must delete+recreate)
func (n *Node) writeIntent(cs *ChangeSet, op, resource string, params map[string]string, parents []string) error {
	// Normalize nil parents to empty slice for consistent comparison
	if parents == nil {
		parents = []string{}
	}

	// Check for existing intent — idempotent update or parent mismatch
	existing := n.GetIntent(resource)
	if existing != nil {
		existingParents := existing.Parents
		if existingParents == nil {
			existingParents = []string{}
		}
		if !slicesEqual(parents, existingParents) {
			return fmt.Errorf("writeIntent %q: parents mismatch (existing %v, requested %v) — delete and recreate to change parents",
				resource, existingParents, parents)
		}
		// Same parents — idempotent update: replace record (DEL+HSET) so
		// dropped params don't orphan in projection / CONFIG_DB. Without
		// the DEL, mergeHydrator leaves any field that the existing record
		// had but the new params dropped, surfacing as intent-vs-state
		// divergence. CLAUDE.md "CONFIG_DB Replace Semantics (DEL+HSET)".
		// Issue #228.
		intent := &sonic.Intent{
			Resource:  resource,
			Operation: op,
			State:     sonic.IntentActuated,
			Parents:   parents,
			Children:  existing.Children, // preserve existing children
			Params:    params,
		}
		fields := intent.ToFields()
		// Prepend ADD first so it sits at position 0; then prepend the
		// DELETE before it so the apply order is DEL → SET. Two prepends
		// reverse: second goes BEFORE first.
		cs.Prepend("NEWTRON_INTENT", resource, fields)
		cs.Changes = append([]Change{{Table: "NEWTRON_INTENT", Key: resource, Type: ChangeDelete}}, cs.Changes...)
		// Clear the in-memory projection so the subsequent renderIntent's
		// mergeHydrator starts from a fresh map — mirrors the DEL the
		// CONFIG_DB will see.
		delete(n.configDB.NewtronIntent, resource)
		n.renderIntent(sonic.Entry{Table: "NEWTRON_INTENT", Key: resource, Fields: fields})
		n.unsavedIntents = true
		return nil
	}

	// New intent — verify all parents exist (I4)
	for _, p := range parents {
		parentIntent := n.GetIntent(p)
		if parentIntent == nil {
			return fmt.Errorf("writeIntent %q: parent %q does not exist", resource, p)
		}
	}

	// Register child with each parent: append resource to parent's _children
	for _, p := range parents {
		parentIntent := n.GetIntent(p)
		if parentIntent == nil {
			continue // should not happen — checked above
		}
		parentIntent.Children = appendUnique(parentIntent.Children, resource)
		parentFields := parentIntent.ToFields()
		cs.Add("NEWTRON_INTENT", p, parentFields)
		n.renderIntent(sonic.Entry{Table: "NEWTRON_INTENT", Key: p, Fields: parentFields})
	}

	// Create the intent record
	intent := &sonic.Intent{
		Resource:  resource,
		Operation: op,
		State:     sonic.IntentActuated,
		Parents:   parents,
		Params:    params,
	}
	fields := intent.ToFields()
	cs.Prepend("NEWTRON_INTENT", resource, fields)
	n.renderIntent(sonic.Entry{Table: "NEWTRON_INTENT", Key: resource, Fields: fields})
	n.unsavedIntents = true
	return nil
}

// deleteIntent removes a NEWTRON_INTENT record and deregisters from all parents.
// Returns error if the record has children (I5 — must remove children first).
func (n *Node) deleteIntent(cs *ChangeSet, resource string) error {
	intent := n.GetIntent(resource)
	if intent == nil {
		// Record doesn't exist — nothing to delete
		return nil
	}

	// I5: refuse if children exist
	if len(intent.Children) > 0 {
		return fmt.Errorf("deleteIntent %q: has children %v", resource, intent.Children)
	}

	// Deregister from each parent: remove resource from parent's _children
	for _, p := range intent.Parents {
		parentIntent := n.GetIntent(p)
		if parentIntent == nil {
			continue // parent already gone — stale reference
		}
		parentIntent.Children = removeItem(parentIntent.Children, resource)
		parentFields := parentIntent.ToFields()
		cs.Add("NEWTRON_INTENT", p, parentFields)
		n.renderIntent(sonic.Entry{Table: "NEWTRON_INTENT", Key: p, Fields: parentFields})
	}

	// Delete own intent record
	cs.Delete("NEWTRON_INTENT", resource)
	n.configDB.DeleteEntry("NEWTRON_INTENT", resource)
	n.unsavedIntents = true
	return nil
}

// renderIntent updates the projection with an intent entry.
// In both online and offline modes, intent records are written to the
// projection immediately so that subsequent writeIntent calls within the same
// operation can see parent intents (I4 enforcement requires parents to exist).
func (n *Node) renderIntent(entry sonic.Entry) {
	n.configDB.ApplyEntries([]sonic.Entry{entry})
}

// slicesEqual returns true if two string slices contain the same elements in the same order.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// appendUnique appends item to slice if not already present.
func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

// removeItem returns a new slice with item removed.
func removeItem(slice []string, item string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}


// intentKind extracts the kind prefix from a resource key.
// "interface|Ethernet0|qos" → "interface", "device" → "device"
func intentKind(resource string) string {
	parts := strings.SplitN(resource, "|", 2)
	return parts[0]
}

