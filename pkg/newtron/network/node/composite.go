// composite.go implements offline composite CONFIG_DB generation and atomic delivery.
package node

import (
	"fmt"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
)

// CompositeMode defines the delivery mode for composite configs.
type CompositeMode string

const (
	// CompositeOverwrite replaces the entire CONFIG_DB with composite content.
	// Used for initial device provisioning and lab setup.
	CompositeOverwrite CompositeMode = "overwrite"

	// CompositeMerge adds entries to existing CONFIG_DB.
	// Only supported for interface-level service configuration,
	// and only if the target interface has no existing service binding.
	CompositeMerge CompositeMode = "merge"
)

// CompositeConfig represents a composite CONFIG_DB configuration generated offline.
type CompositeConfig struct {
	// Tables holds the composite config: table -> key -> field -> value
	Tables   map[string]map[string]map[string]string `json:"tables"`
	Metadata CompositeMetadata                        `json:"metadata"`
}

// CompositeMetadata contains provenance information for a composite config.
type CompositeMetadata struct {
	Timestamp   time.Time     `json:"timestamp"`
	NetworkName string        `json:"network_name,omitempty"`
	DeviceName  string        `json:"device_name,omitempty"`
	Mode        CompositeMode `json:"mode"`
	GeneratedBy string        `json:"generated_by,omitempty"`
	Description string        `json:"description,omitempty"`
}

// CompositeDeliveryResult reports the outcome of delivering a composite config.
type CompositeDeliveryResult struct {
	Applied int           `json:"applied"` // Number of entries written
	Skipped int           `json:"skipped"` // Number of entries skipped (already exist in merge)
	Failed  int           `json:"failed"`  // Number of entries that failed
	Error   error         `json:"error,omitempty"`
	Mode    CompositeMode `json:"mode"`
}

// CompositeBuilder constructs composite configs offline using a builder pattern.
// All Add* methods accumulate entries without requiring a device connection.
type CompositeBuilder struct {
	tables   map[string]map[string]map[string]string
	metadata CompositeMetadata
}

// NewCompositeBuilder creates a new composite builder for the given device and mode.
func NewCompositeBuilder(deviceName string, mode CompositeMode) *CompositeBuilder {
	return &CompositeBuilder{
		tables: make(map[string]map[string]map[string]string),
		metadata: CompositeMetadata{
			Timestamp:  time.Now(),
			DeviceName: deviceName,
			Mode:       mode,
		},
	}
}

// SetDescription sets the description in metadata.
func (cb *CompositeBuilder) SetDescription(desc string) *CompositeBuilder {
	cb.metadata.Description = desc
	return cb
}

// SetGeneratedBy sets the generator identifier in metadata.
func (cb *CompositeBuilder) SetGeneratedBy(by string) *CompositeBuilder {
	cb.metadata.GeneratedBy = by
	return cb
}

// AddEntries adds multiple CompositeEntry values to the composite.
func (cb *CompositeBuilder) AddEntries(entries []CompositeEntry) *CompositeBuilder {
	for _, e := range entries {
		cb.AddEntry(e.Table, e.Key, e.Fields)
	}
	return cb
}

// AddEntry adds a single CONFIG_DB entry to the composite.
func (cb *CompositeBuilder) AddEntry(table, key string, fields map[string]string) *CompositeBuilder {
	if cb.tables[table] == nil {
		cb.tables[table] = make(map[string]map[string]string)
	}
	if cb.tables[table][key] == nil {
		cb.tables[table][key] = make(map[string]string)
	}
	for k, v := range fields {
		cb.tables[table][key][k] = v
	}
	return cb
}

// Build returns the completed CompositeConfig.
func (cb *CompositeBuilder) Build() *CompositeConfig {
	return &CompositeConfig{
		Tables:   cb.tables,
		Metadata: cb.metadata,
	}
}

// EntryCount returns the total number of entries in the composite config.
func (cc *CompositeConfig) EntryCount() int {
	count := 0
	for _, keys := range cc.Tables {
		count += len(keys)
	}
	return count
}

// ToConfigChanges converts the composite config to a slice of device.ConfigChange
// (all as ChangeTypeAdd) for use with VerifyChangeSet.
func (cc *CompositeConfig) ToConfigChanges() []sonic.ConfigChange {
	var changes []sonic.ConfigChange
	for table, keys := range cc.Tables {
		for key, fields := range keys {
			changes = append(changes, sonic.ConfigChange{
				Table:  table,
				Key:    key,
				Type:   sonic.ChangeTypeAdd,
				Fields: fields,
			})
		}
	}
	return changes
}

// ToTableChanges converts the composite config to a slice of sonic.TableChange
// for pipeline delivery.
func (cc *CompositeConfig) ToTableChanges() []sonic.TableChange {
	var changes []sonic.TableChange
	for table, keys := range cc.Tables {
		for key, fields := range keys {
			changes = append(changes, sonic.TableChange{
				Table:  table,
				Key:    key,
				Fields: fields,
			})
		}
	}
	return changes
}

// DeliverComposite delivers a composite config to a device.
// For overwrite mode: replaces entire CONFIG_DB.
// For merge mode: validates no conflicts, then pipeline-writes new entries.
func (n *Node) DeliverComposite(composite *CompositeConfig, mode CompositeMode) (*CompositeDeliveryResult, error) {
	if err := n.precondition("deliver-composite", string(mode)).Result(); err != nil {
		return nil, err
	}

	result := &CompositeDeliveryResult{Mode: mode}
	changes := composite.ToTableChanges()

	client := n.conn.Client()

	switch mode {
	case CompositeOverwrite:
		err := client.ReplaceAll(changes)
		if err != nil {
			result.Error = err
			result.Failed = len(changes)
			return result, err
		}
		result.Applied = len(changes)

	case CompositeMerge:
		// Validate: no existing service bindings for merge targets
		if err := n.validateMerge(composite); err != nil {
			return nil, err
		}

		err := client.PipelineSet(changes)
		if err != nil {
			result.Error = err
			result.Failed = len(changes)
			return result, err
		}
		result.Applied = len(changes)

	default:
		return nil, fmt.Errorf("unknown composite mode: %s", mode)
	}

	return result, nil
}

// validateMerge checks that merge won't conflict with existing config.
// Merge is only supported for interface-level service configuration,
// and only if the target interface has no existing service binding.
func (n *Node) validateMerge(composite *CompositeConfig) error {
	configDB := n.ConfigDB()
	if configDB == nil {
		return fmt.Errorf("config_db not loaded")
	}

	// Check for existing service bindings on interfaces being merged
	if bindings, ok := composite.Tables["NEWTRON_SERVICE_BINDING"]; ok {
		for intfName := range bindings {
			if existing, exists := configDB.NewtronServiceBinding[intfName]; exists {
				return fmt.Errorf("interface %s already has service '%s' bound — remove existing service before merge",
					intfName, existing.ServiceName)
			}
		}
	}

	return nil
}
