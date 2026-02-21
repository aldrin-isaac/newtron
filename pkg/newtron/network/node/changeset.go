package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device"
	"github.com/newtron-network/newtron/pkg/util"
)

// ChangeType is an alias for device.ChangeType, re-exported for convenience.
// All new code should prefer device.ChangeType directly.
type ChangeType = device.ChangeType

// Re-export device.ChangeType constants so existing code compiles without changes.
const (
	ChangeAdd    = device.ChangeTypeAdd
	ChangeModify = device.ChangeTypeModify
	ChangeDelete = device.ChangeTypeDelete
)

// Change represents a single configuration change.
type Change struct {
	Table    string            `json:"table"`
	Key      string            `json:"key"`
	Type     device.ChangeType `json:"type"`
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
	Verification *device.VerificationResult `json:"verification,omitempty"`   // populated after apply+verify in execute mode
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
func (cs *ChangeSet) Add(table, key string, changeType device.ChangeType, oldValue, newValue map[string]string) {
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
// Bridges pure config functions (return []CompositeEntry) with the ChangeSet
// world used by primitives and composites.
func configToChangeSet(deviceName, operation string, config []CompositeEntry, changeType device.ChangeType) *ChangeSet {
	cs := NewChangeSet(deviceName, operation)
	for _, e := range config {
		cs.Add(e.Table, e.Key, changeType, nil, e.Fields)
	}
	return cs
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

// toDeviceChanges converts the ChangeSet's Changes to device.ConfigChange slice.
// Used by both Apply() and Verify() to avoid duplicating the conversion logic.
func (cs *ChangeSet) toDeviceChanges() []device.ConfigChange {
	deviceChanges := make([]device.ConfigChange, 0, len(cs.Changes))
	for _, c := range cs.Changes {
		deviceChanges = append(deviceChanges, device.ConfigChange{
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

	if err := n.Underlying().ApplyChanges(cs.toDeviceChanges()); err != nil {
		return err
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

	result, err := n.Underlying().VerifyChangeSet(context.Background(), cs.toDeviceChanges())
	if err != nil {
		return err
	}
	cs.Verification = result
	return nil
}
