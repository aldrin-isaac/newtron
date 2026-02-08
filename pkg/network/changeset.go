package network

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/device"
)

// ChangeType represents the type of configuration change.
type ChangeType string

const (
	ChangeAdd    ChangeType = "add"
	ChangeModify ChangeType = "modify"
	ChangeDelete ChangeType = "delete"
)

// Change represents a single configuration change.
type Change struct {
	Table    string            `json:"table"`
	Key      string            `json:"key"`
	Type     ChangeType        `json:"type"`
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
func (cs *ChangeSet) Add(table, key string, changeType ChangeType, oldValue, newValue map[string]string) {
	cs.Changes = append(cs.Changes, Change{
		Table:    table,
		Key:      key,
		Type:     changeType,
		OldValue: oldValue,
		NewValue: newValue,
	})
}

// IsEmpty returns true if there are no changes.
func (cs *ChangeSet) IsEmpty() bool {
	return len(cs.Changes) == 0
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

// Apply writes the changes to the device's config_db via Redis.
func (cs *ChangeSet) Apply(d *Device) error {
	if !d.IsConnected() {
		return fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return fmt.Errorf("device not locked - call Lock() first")
	}

	// Convert network.Change to device.ConfigChange
	deviceChanges := make([]device.ConfigChange, 0, len(cs.Changes))
	for _, c := range cs.Changes {
		dc := device.ConfigChange{
			Table:  c.Table,
			Key:    c.Key,
			Fields: c.NewValue,
		}
		switch c.Type {
		case ChangeAdd:
			dc.Type = device.ChangeTypeAdd
		case ChangeModify:
			dc.Type = device.ChangeTypeModify
		case ChangeDelete:
			dc.Type = device.ChangeTypeDelete
		}
		deviceChanges = append(deviceChanges, dc)
	}

	// Apply via device's Redis client
	if err := d.Underlying().ApplyChanges(deviceChanges); err != nil {
		return err
	}
	cs.AppliedCount = len(cs.Changes)
	return nil
}

// Verify re-reads CONFIG_DB via a fresh connection and compares against the
// ChangeSet to confirm that writes were persisted. Stores the result in
// cs.Verification.
func (cs *ChangeSet) Verify(d *Device) error {
	if !d.IsConnected() {
		return fmt.Errorf("device not connected")
	}

	// Convert network.Change to device.ConfigChange for verification
	deviceChanges := make([]device.ConfigChange, 0, len(cs.Changes))
	for _, c := range cs.Changes {
		dc := device.ConfigChange{
			Table:  c.Table,
			Key:    c.Key,
			Fields: c.NewValue,
		}
		switch c.Type {
		case ChangeAdd:
			dc.Type = device.ChangeTypeAdd
		case ChangeModify:
			dc.Type = device.ChangeTypeModify
		case ChangeDelete:
			dc.Type = device.ChangeTypeDelete
		}
		deviceChanges = append(deviceChanges, dc)
	}

	result, err := d.Underlying().VerifyChangeSet(context.Background(), deviceChanges)
	if err != nil {
		return err
	}
	cs.Verification = result
	return nil
}

// Rollback applies the inverse of each applied change in reverse order.
// ChangeAdd → delete the table/key, ChangeModify → restore OldValue,
// ChangeDelete → recreate with OldValue. Best-effort: attempts ALL inverse
// operations, collecting errors via errors.Join(). Caller should verify
// device state after rollback.
func (cs *ChangeSet) Rollback(d *Device) error {
	if cs.AppliedCount == 0 {
		return nil
	}
	if !d.IsConnected() {
		return fmt.Errorf("device not connected")
	}
	if !d.IsLocked() {
		return fmt.Errorf("device not locked - call Lock() first")
	}

	var errs []error

	// Iterate applied changes in reverse order
	for i := cs.AppliedCount - 1; i >= 0; i-- {
		c := cs.Changes[i]

		var inverse device.ConfigChange
		switch c.Type {
		case ChangeAdd:
			// Inverse of add → delete
			inverse = device.ConfigChange{
				Table: c.Table,
				Key:   c.Key,
				Type:  device.ChangeTypeDelete,
			}
		case ChangeModify:
			// Inverse of modify → restore OldValue
			inverse = device.ConfigChange{
				Table:  c.Table,
				Key:    c.Key,
				Type:   device.ChangeTypeModify,
				Fields: c.OldValue,
			}
		case ChangeDelete:
			// Inverse of delete → recreate with OldValue
			inverse = device.ConfigChange{
				Table:  c.Table,
				Key:    c.Key,
				Type:   device.ChangeTypeAdd,
				Fields: c.OldValue,
			}
		}

		if err := d.Underlying().ApplyChanges([]device.ConfigChange{inverse}); err != nil {
			errs = append(errs, fmt.Errorf("rollback %s|%s: %w", c.Table, c.Key, err))
		}
	}

	return errors.Join(errs...)
}
