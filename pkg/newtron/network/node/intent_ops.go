package node

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
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
		// Same parents — idempotent update: replace params, preserve _children
		intent := &sonic.Intent{
			Resource:  resource,
			Operation: op,
			State:     sonic.IntentActuated,
			Parents:   parents,
			Children:  existing.Children, // preserve existing children
			Params:    params,
		}
		fields := intent.ToFields()
		cs.Prepend("NEWTRON_INTENT", resource, fields)
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

// ============================================================================
// DAG Health Validation (§11)
// ============================================================================

// DAGViolation describes a structural issue in the intent DAG.
type DAGViolation struct {
	Resource string // The intent record with the issue
	Kind     string // "bidirectional", "dangling_parent", "dangling_child", "orphan"
	Message  string
}

// ValidateIntentDAG checks the structural integrity of the intent DAG.
// Returns violations for:
//   - I2: Bidirectional inconsistency (parent lists child but child doesn't list parent, or vice versa)
//   - I3: Referential integrity (parent or child references a nonexistent record)
//   - Orphans: Records not reachable from the "device" root
func ValidateIntentDAG(configDB *sonic.ConfigDB) []DAGViolation {
	if configDB == nil {
		return nil
	}
	intents := configDB.NewtronIntent
	var violations []DAGViolation

	// Parse all intent records
	parsed := make(map[string]*sonic.Intent, len(intents))
	for resource, fields := range intents {
		parsed[resource] = sonic.NewIntent(resource, fields)
	}

	for resource, intent := range parsed {
		// I3 + I2: Check parent references
		for _, parent := range intent.Parents {
			parentIntent, exists := parsed[parent]
			if !exists {
				violations = append(violations, DAGViolation{
					Resource: resource,
					Kind:     "dangling_parent",
					Message:  fmt.Sprintf("parent %q does not exist", parent),
				})
				continue
			}
			// I2: bidirectional — parent must list this resource as a child
			found := false
			for _, child := range parentIntent.Children {
				if child == resource {
					found = true
					break
				}
			}
			if !found {
				violations = append(violations, DAGViolation{
					Resource: resource,
					Kind:     "bidirectional",
					Message:  fmt.Sprintf("parent %q does not list %q as child", parent, resource),
				})
			}
		}

		// I3 + I2: Check child references
		for _, child := range intent.Children {
			childIntent, exists := parsed[child]
			if !exists {
				violations = append(violations, DAGViolation{
					Resource: resource,
					Kind:     "dangling_child",
					Message:  fmt.Sprintf("child %q does not exist", child),
				})
				continue
			}
			// I2: bidirectional — child must list this resource as a parent
			found := false
			for _, parent := range childIntent.Parents {
				if parent == resource {
					found = true
					break
				}
			}
			if !found {
				violations = append(violations, DAGViolation{
					Resource: resource,
					Kind:     "bidirectional",
					Message:  fmt.Sprintf("child %q does not list %q as parent", child, resource),
				})
			}
		}
	}

	// Orphan detection: BFS from "device" root
	if _, hasRoot := parsed["device"]; hasRoot {
		reachable := make(map[string]bool)
		queue := []string{"device"}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if reachable[current] {
				continue
			}
			reachable[current] = true
			if intent, ok := parsed[current]; ok {
				queue = append(queue, intent.Children...)
			}
		}
		for resource := range parsed {
			if !reachable[resource] {
				violations = append(violations, DAGViolation{
					Resource: resource,
					Kind:     "orphan",
					Message:  "not reachable from device root",
				})
			}
		}
	}

	return violations
}
