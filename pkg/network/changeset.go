package network

import (
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
	Device    string    `json:"device"`
	Operation string    `json:"operation"`
	Timestamp time.Time `json:"timestamp"`
	Changes   []Change  `json:"changes"`
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
	return d.Underlying().ApplyChanges(deviceChanges)
}
