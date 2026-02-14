// composite.go implements offline composite CONFIG_DB generation and atomic delivery.
package network

import (
	"fmt"
	"time"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/util"
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

// SetNetwork sets the network name in metadata.
func (cb *CompositeBuilder) SetNetwork(name string) *CompositeBuilder {
	cb.metadata.NetworkName = name
	return cb
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

// AddBGPGlobals adds BGP global settings to the composite.
func (cb *CompositeBuilder) AddBGPGlobals(vrf string, fields map[string]string) *CompositeBuilder {
	return cb.AddEntry("BGP_GLOBALS", vrf, fields)
}

// AddBGPGlobalsAF adds BGP address-family settings to the composite.
func (cb *CompositeBuilder) AddBGPGlobalsAF(vrf, af string, fields map[string]string) *CompositeBuilder {
	key := fmt.Sprintf("%s|%s", vrf, af)
	return cb.AddEntry("BGP_GLOBALS_AF", key, fields)
}

// AddBGPNeighbor adds a BGP neighbor to the composite.
// Key format: vrf|neighborIP (per SONiC Unified FRR Mgmt schema for VRF-aware tables).
func (cb *CompositeBuilder) AddBGPNeighbor(vrf, neighborIP string, fields map[string]string) *CompositeBuilder {
	key := fmt.Sprintf("%s|%s", vrf, neighborIP)
	return cb.AddEntry("BGP_NEIGHBOR", key, fields)
}

// AddBGPNeighborAF adds BGP neighbor address-family settings.
// Key format: vrf|neighborIP|af (per SONiC Unified FRR Mgmt schema for VRF-aware tables).
func (cb *CompositeBuilder) AddBGPNeighborAF(vrf, neighborIP, af string, fields map[string]string) *CompositeBuilder {
	key := fmt.Sprintf("%s|%s|%s", vrf, neighborIP, af)
	return cb.AddEntry("BGP_NEIGHBOR_AF", key, fields)
}

// AddPeerGroup adds a BGP peer group to the composite.
func (cb *CompositeBuilder) AddPeerGroup(name string, fields map[string]string) *CompositeBuilder {
	return cb.AddEntry("BGP_PEER_GROUP", name, fields)
}

// AddPortConfig adds a PORT entry to the composite.
func (cb *CompositeBuilder) AddPortConfig(portName string, fields map[string]string) *CompositeBuilder {
	return cb.AddEntry("PORT", portName, fields)
}

// AddService adds all CONFIG_DB entries for a service application to an interface.
// This builds the same entries that Interface.ApplyService() would create,
// but without requiring a device connection.
func (cb *CompositeBuilder) AddService(interfaceName, serviceName string, fields map[string]string) *CompositeBuilder {
	// Record the service binding
	bindingFields := map[string]string{
		"service_name": serviceName,
	}
	for k, v := range fields {
		bindingFields[k] = v
	}
	return cb.AddEntry("NEWTRON_SERVICE_BINDING", interfaceName, bindingFields)
}

// AddRouteRedistribution adds a route redistribution entry.
// Key format: vrf|src_protocol|dst_protocol|addr_family (per SONiC Unified FRR Mgmt HLD).
// dst_protocol is always "bgp" (the only supported destination).
func (cb *CompositeBuilder) AddRouteRedistribution(vrf, protocol, af string, fields map[string]string) *CompositeBuilder {
	key := fmt.Sprintf("%s|%s|bgp|%s", vrf, protocol, af)
	return cb.AddEntry("ROUTE_REDISTRIBUTE", key, fields)
}

// AddRouteMap adds a route-map entry.
func (cb *CompositeBuilder) AddRouteMap(name string, seq int, fields map[string]string) *CompositeBuilder {
	key := fmt.Sprintf("%s|%d", name, seq)
	return cb.AddEntry("ROUTE_MAP", key, fields)
}

// AddPrefixSet adds a prefix-set entry.
func (cb *CompositeBuilder) AddPrefixSet(name string, seq int, fields map[string]string) *CompositeBuilder {
	key := fmt.Sprintf("%s|%d", name, seq)
	return cb.AddEntry("PREFIX_SET", key, fields)
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

// ToTableChanges converts the composite config to a slice of device.TableChange
// for pipeline delivery.
func (cc *CompositeConfig) ToTableChanges() []device.TableChange {
	var changes []device.TableChange
	for table, keys := range cc.Tables {
		for key, fields := range keys {
			changes = append(changes, device.TableChange{
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
func (d *Device) DeliverComposite(composite *CompositeConfig, mode CompositeMode) (*CompositeDeliveryResult, error) {
	if err := d.precondition("deliver-composite", string(mode)).Result(); err != nil {
		return nil, err
	}

	result := &CompositeDeliveryResult{Mode: mode}
	changes := composite.ToTableChanges()

	client := d.Underlying().Client()

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
		if err := d.validateMerge(composite); err != nil {
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

// ValidateComposite performs a dry-run validation of composite delivery without applying.
func (d *Device) ValidateComposite(composite *CompositeConfig, mode CompositeMode) error {
	if !d.IsConnected() {
		return util.ErrNotConnected
	}

	switch mode {
	case CompositeOverwrite:
		// Overwrite always valid (replaces everything)
		return nil
	case CompositeMerge:
		return d.validateMerge(composite)
	default:
		return fmt.Errorf("unknown composite mode: %s", mode)
	}
}

// validateMerge checks that merge won't conflict with existing config.
// Merge is only supported for interface-level service configuration,
// and only if the target interface has no existing service binding.
func (d *Device) validateMerge(composite *CompositeConfig) error {
	configDB := d.ConfigDB()
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
