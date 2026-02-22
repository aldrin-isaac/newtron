package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// ChangeType is an alias for sonic.ChangeType, re-exported for convenience.
// All new code should prefer sonic.ChangeType directly.
type ChangeType = sonic.ChangeType

// Re-export sonic.ChangeType constants so existing code compiles without changes.
const (
	ChangeAdd    = sonic.ChangeTypeAdd
	ChangeModify = sonic.ChangeTypeModify
	ChangeDelete = sonic.ChangeTypeDelete
)

// Change represents a single configuration change.
type Change struct {
	Table    string            `json:"table"`
	Key      string            `json:"key"`
	Type     sonic.ChangeType `json:"type"`
	OldValue map[string]string `json:"old_value,omitempty"`
	NewValue map[string]string `json:"new_value,omitempty"`
}

// ChangeSet represents a collection of configuration changes.
type ChangeSet struct {
	Device       string                     `json:"device"`
	Operation    string                     `json:"operation"`
	Timestamp    time.Time                  `json:"timestamp"`
	Changes      []Change                   `json:"changes"`
	AppliedCount int                        `json:"applied_count"`            // number of changes successfully written by Apply(); 0 before Apply()
	Verification *sonic.VerificationResult `json:"verification,omitempty"`   // populated after apply+verify in execute mode
}

// NewChangeSet creates a new ChangeSet.
func NewChangeSet(device, operation string) *ChangeSet {
	return &ChangeSet{
		Device:    device,
		Operation: operation,
		Timestamp: time.Now(),
		Changes:   make([]Change, 0),
	}
}

// Add adds a change to the set.
func (cs *ChangeSet) Add(table, key string, changeType sonic.ChangeType, oldValue, newValue map[string]string) {
	cs.Changes = append(cs.Changes, Change{
		Table:    table,
		Key:      key,
		Type:     changeType,
		OldValue: oldValue,
		NewValue: newValue,
	})
}

// Merge appends all changes from other into cs.
func (cs *ChangeSet) Merge(other *ChangeSet) {
	cs.Changes = append(cs.Changes, other.Changes...)
}

// IsEmpty returns true if there are no changes.
func (cs *ChangeSet) IsEmpty() bool {
	return len(cs.Changes) == 0
}

// configToChangeSet wraps config function output into a ChangeSet.
// Bridges pure config functions (return []sonic.Entry) with the ChangeSet
// world used by primitives and composites.
func configToChangeSet(deviceName, operation string, config []sonic.Entry, changeType sonic.ChangeType) *ChangeSet {
	cs := NewChangeSet(deviceName, operation)
	for _, e := range config {
		cs.Add(e.Table, e.Key, changeType, nil, e.Fields)
	}
	return cs
}

// op is a generic helper for simple CRUD operations. It runs precondition
// checks, calls the entry generator, and wraps the result in a ChangeSet.
// Use this for operations whose entire body is: preconditions → generate entries → done.
// Skip it for complex operations that need custom logic between precondition and return
// (e.g., ApplyService, RemoveService, SetupEVPN).
func (n *Node) op(name, resource string, changeType sonic.ChangeType,
	checks func(*PreconditionChecker), gen func() []sonic.Entry) (*ChangeSet, error) {

	pc := n.precondition(name, resource)
	if checks != nil {
		checks(pc)
	}
	if err := pc.Result(); err != nil {
		return nil, err
	}
	return configToChangeSet(n.name, "device."+name, gen(), changeType), nil
}

// String returns a human-readable representation of the changes.
func (cs *ChangeSet) String() string {
	if cs.IsEmpty() {
		return "No changes"
	}

	var sb strings.Builder
	for _, c := range cs.Changes {
		typeStr := ""
		switch c.Type {
		case ChangeAdd:
			typeStr = "[ADD]"
		case ChangeModify:
			typeStr = "[MOD]"
		case ChangeDelete:
			typeStr = "[DEL]"
		}

		sb.WriteString(fmt.Sprintf("  %s %s|%s", typeStr, c.Table, c.Key))
		if c.NewValue != nil && len(c.NewValue) > 0 {
			sb.WriteString(fmt.Sprintf(" → %v", c.NewValue))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// Preview returns a formatted preview of the changes.
func (cs *ChangeSet) Preview() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Operation: %s\n", cs.Operation))
	sb.WriteString(fmt.Sprintf("Device: %s\n", cs.Device))
	sb.WriteString(fmt.Sprintf("Changes:\n%s", cs.String()))
	return sb.String()
}

// toChanges converts the ChangeSet's Changes to []sonic.ConfigChange for Apply and Verify.
// Used by both Apply() and Verify() to avoid duplicating the conversion logic.
func (cs *ChangeSet) toDeviceChanges() []sonic.ConfigChange {
	deviceChanges := make([]sonic.ConfigChange, 0, len(cs.Changes))
	for _, c := range cs.Changes {
		deviceChanges = append(deviceChanges, sonic.ConfigChange{
			Table:  c.Table,
			Key:    c.Key,
			Type:   c.Type,
			Fields: c.NewValue,
		})
	}
	return deviceChanges
}

// Apply writes the changes to the device's config_db via Redis.
func (cs *ChangeSet) Apply(n *Node) error {
	if err := n.precondition("apply-changeset", cs.Operation).Result(); err != nil {
		return err
	}

	client := n.ConfigDBClient()
	if client == nil {
		return fmt.Errorf("CONFIG_DB client not connected")
	}

	for _, change := range cs.toDeviceChanges() {
		var err error
		switch change.Type {
		case sonic.ChangeTypeAdd, sonic.ChangeTypeModify:
			err = client.Set(change.Table, change.Key, change.Fields)
		case sonic.ChangeTypeDelete:
			err = client.Delete(change.Table, change.Key)
		}
		if err != nil {
			return fmt.Errorf("applying change to %s|%s: %w", change.Table, change.Key, err)
		}
	}

	cs.AppliedCount = len(cs.Changes)
	return nil
}

// Verify re-reads CONFIG_DB via a fresh connection and compares against the
// ChangeSet to confirm that writes were persisted. Stores the result in
// cs.Verification.
func (cs *ChangeSet) Verify(n *Node) error {
	if !n.IsConnected() {
		return util.ErrNotConnected
	}

	result, err := n.verifyConfigChanges(cs.toDeviceChanges())
	if err != nil {
		return err
	}
	cs.Verification = result
	return nil
}

// verifyConfigChanges re-reads CONFIG_DB via a fresh connection and compares
// against the given changes. Used by both ChangeSet.Verify and VerifyComposite.
func (n *Node) verifyConfigChanges(changes []sonic.ConfigChange) (*sonic.VerificationResult, error) {
	if n.conn == nil {
		return nil, util.ErrNotConnected
	}

	addr := n.conn.ConnAddr()

	freshClient := sonic.NewConfigDBClient(addr)
	if err := freshClient.Connect(); err != nil {
		return nil, fmt.Errorf("fresh config_db connection: %w", err)
	}
	defer freshClient.Close()

	result := &sonic.VerificationResult{}

	for _, change := range changes {
		switch change.Type {
		case sonic.ChangeTypeAdd, sonic.ChangeTypeModify:
			actual, err := freshClient.Get(change.Table, change.Key)
			if err != nil {
				return nil, fmt.Errorf("reading %s|%s: %w", change.Table, change.Key, err)
			}
			if len(actual) == 0 {
				result.Failed++
				result.Errors = append(result.Errors, sonic.VerificationError{
					Table:    change.Table,
					Key:      change.Key,
					Field:    "(all)",
					Expected: "present",
					Actual:   "",
				})
				continue
			}
			allMatch := true
			for field, expected := range change.Fields {
				if got, ok := actual[field]; !ok || got != expected {
					result.Failed++
					allMatch = false
					actualVal := ""
					if ok {
						actualVal = got
					}
					result.Errors = append(result.Errors, sonic.VerificationError{
						Table:    change.Table,
						Key:      change.Key,
						Field:    field,
						Expected: expected,
						Actual:   actualVal,
					})
				}
			}
			if allMatch {
				result.Passed++
			}
		case sonic.ChangeTypeDelete:
			exists, err := freshClient.Exists(change.Table, change.Key)
			if err != nil {
				return nil, fmt.Errorf("checking %s|%s: %w", change.Table, change.Key, err)
			}
			if exists {
				result.Failed++
				result.Errors = append(result.Errors, sonic.VerificationError{
					Table:    change.Table,
					Key:      change.Key,
					Field:    "(all)",
					Expected: "deleted",
					Actual:   "present",
				})
			} else {
				result.Passed++
			}
		}
	}

	return result, nil
}

// VerifyComposite verifies a composite config against live CONFIG_DB.
// Used by topology health checks to verify the expected composite state.
func (n *Node) VerifyComposite(ctx context.Context, composite *CompositeConfig) (*sonic.VerificationResult, error) {
	return n.verifyConfigChanges(composite.ToConfigChanges())
}
