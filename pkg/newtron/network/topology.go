// topology.go implements topology-driven operations from topology.json specs.
//
// Uses the Abstract Node pattern: creates an offline Node with a projection,
// calls the same Node/Interface methods used in the online path, and accumulates
// the desired state into the node's typed CONFIG_DB projection. topology.json
// represents an abstract topology in which abstract nodes live — the same code
// path handles both offline provisioning and online operations.
//
// Topology steps are pre-computed, fully-resolved operations in the topology.json
// file. BuildAbstractNode registers ports, then replays steps against an
// abstract Node via node.ReplayStep. One vocabulary: topology steps use the same
// operation names as the API and intent records.
package network

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// TopologyProvisioner generates and delivers configuration from topology specs.
type TopologyProvisioner struct {
	network *Network
}

// NewTopologyProvisioner creates a provisioner from a Network with a loaded topology.
func NewTopologyProvisioner(network *Network) (*TopologyProvisioner, error) {
	if !network.HasTopology() {
		return nil, fmt.Errorf("no topology loaded — ensure topology.json exists in spec directory")
	}
	return &TopologyProvisioner{network: network}, nil
}

// BuildAbstractNode constructs a fully-replayed abstract Node for a device.
// Creates NewAbstract with the device's profile and resolved specs, registers
// ports from topology.Ports, then replays all topology steps via node.ReplayStep.
// Returns the Node with a populated intent DB and projection.
// Returns error for host devices (no SONiC CONFIG_DB) or devices with no steps.
func (tp *TopologyProvisioner) BuildAbstractNode(deviceName string) (*node.Node, error) {
	if tp.network.IsHostDevice(deviceName) {
		return nil, fmt.Errorf("device '%s' is a host — cannot build abstract node", deviceName)
	}
	topoDev, err := tp.network.GetTopologyDevice(deviceName)
	if err != nil {
		return nil, err
	}

	if len(topoDev.Steps) == 0 {
		return nil, fmt.Errorf("device '%s' has no provisioning steps in topology.json", deviceName)
	}

	// Load and resolve device profile
	profile, err := tp.network.loadProfile(deviceName)
	if err != nil {
		return nil, fmt.Errorf("loading profile: %w", err)
	}
	resolved, err := tp.network.resolveProfile(deviceName, profile)
	if err != nil {
		return nil, fmt.Errorf("resolving profile: %w", err)
	}

	// Build per-device ResolvedSpecs for hierarchical spec lookups
	resolvedSpecs := tp.network.buildResolvedSpecs(profile)

	ctx := context.Background()

	// Create abstract node with empty projection.
	// Operations build desired state; Reconcile delivers it.
	n := node.NewAbstract(resolvedSpecs, deviceName, profile, resolved)

	// Register physical ports (enables GetInterface for interface-scoped steps)
	for portName, fields := range topoDev.Ports {
		n.RegisterPort(portName, fields)
	}

	// Replay each step against the abstract node
	for i, step := range topoDev.Steps {
		if err := node.ReplayStep(ctx, n, step); err != nil {
			return nil, fmt.Errorf("step[%d] %s: %w", i, step.URL, err)
		}
	}

	// Replay is loaded state, not new mutations — clear the flag that
	// writeIntent sets during replay so the unsaved guard doesn't fire.
	n.ClearUnsavedIntents()

	return n, nil
}

// BuildEmptyAbstractNode constructs an abstract Node for a device without replaying steps.
// Creates NewAbstract with the device's profile and resolved specs, registers
// ports from topology.Ports, and returns the node. The intent DB is empty and
// the projection contains only PORT entries from RegisterPort.
// Used when intent-first operations will be applied fresh (not reconstructed from steps).
// Returns error for host devices (no SONiC CONFIG_DB).
func (tp *TopologyProvisioner) BuildEmptyAbstractNode(deviceName string) (*node.Node, error) {
	if tp.network.IsHostDevice(deviceName) {
		return nil, fmt.Errorf("device '%s' is a host — cannot build abstract node", deviceName)
	}
	topoDev, err := tp.network.GetTopologyDevice(deviceName)
	if err != nil {
		return nil, err
	}

	// Load and resolve device profile
	profile, err := tp.network.loadProfile(deviceName)
	if err != nil {
		return nil, fmt.Errorf("loading profile: %w", err)
	}
	resolved, err := tp.network.resolveProfile(deviceName, profile)
	if err != nil {
		return nil, fmt.Errorf("resolving profile: %w", err)
	}

	// Build per-device ResolvedSpecs for hierarchical spec lookups
	resolvedSpecs := tp.network.buildResolvedSpecs(profile)

	// Create abstract node with empty projection.
	n := node.NewAbstract(resolvedSpecs, deviceName, profile, resolved)

	// Register physical ports (enables GetInterface for interface-scoped steps)
	for portName, fields := range topoDev.Ports {
		n.RegisterPort(portName, fields)
	}

	return n, nil
}

// SaveDeviceIntents updates a device's topology steps in the loaded topology and
// persists topology.json to disk atomically. Replaces the device's Steps field
// with the provided steps and writes through the spec loader.
func (tp *TopologyProvisioner) SaveDeviceIntents(deviceName string, steps []spec.TopologyStep) error {
	topoDev, err := tp.network.GetTopologyDevice(deviceName)
	if err != nil {
		return err
	}

	// Update the in-memory topology device steps
	topoDev.Steps = steps

	// Persist topology.json atomically via the spec loader
	return tp.network.loader.SaveTopology(tp.network.topology)
}

