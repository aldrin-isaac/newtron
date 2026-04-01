package newtron

import (
	"context"
	"errors"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/network"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// Network is the top-level API entry point.
type Network struct {
	internal *network.Network
	auth     *auth.Checker
}

// LoadNetwork loads all spec files from specDir and returns a Network ready for use.
func LoadNetwork(specDir string) (*Network, error) {
	net, err := network.NewNetwork(specDir)
	if err != nil {
		return nil, err
	}
	return &Network{internal: net}, nil
}

// SetAuth installs a permission checker. If nil, all permission checks are skipped.
func (net *Network) SetAuth(checker *auth.Checker) {
	net.auth = checker
}

// InitDevice prepares a device for newtron management. This is a one-time
// operation that enables unified config mode (frrcfgd) so CONFIG_DB writes
// for BGP, VRF, and other tables are processed by FRR. Without this, SONiC's
// default bgpcfgd silently ignores dynamic CONFIG_DB entries.
//
// The operation:
//  1. Connects to the device (skipping frrcfgd check)
//  2. Writes unified config mode fields to DEVICE_METADATA
//  3. Restarts bgp container so frrcfgd takes over from bgpcfgd
//  4. Saves config to persist across reboots
//
// Safe to run multiple times — no-op if already initialized.
//
// Intentionally one-way: there is no reverse operation. Reverting to bgpcfgd
// would break all newtron CONFIG_DB operations. This is infrastructure init,
// not a reversible configuration change.
//
// ErrAlreadyInitialized is returned by InitDevice when the device already
// has unified config mode enabled. This is not an error condition — the caller
// can display a message and proceed.
var ErrAlreadyInitialized = errors.New("device already initialized")

// ErrActiveConfiguration is returned by InitDevice when the device has active
// BGP configuration and --force was not specified. Initialization switches
// from split/separated mode to unified mode, which:
//   - Restarts the bgp container, dropping all active BGP sessions
//   - Replaces frr.conf with frrcfgd-generated config from CONFIG_DB
//   - Any FRR configuration done via vtysh (not in CONFIG_DB) is permanently lost
//
// The caller should warn the user and retry with Force=true.
var ErrActiveConfiguration = errors.New("device has active BGP configuration; use --force to proceed (this will restart bgp, drop all sessions, and replace frr.conf — any vtysh-only config not in CONFIG_DB will be lost)")

func (net *Network) InitDevice(ctx context.Context, device string, force bool) error {
	dev, err := net.internal.ConnectNodeForSetup(ctx, device)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", device, err)
	}
	defer dev.Disconnect()

	// Remove legacy bgpcfgd-format BGP_NEIGHBOR entries (no VRF prefix).
	// Community sonic-vs ships with 32 such entries in its factory config.
	// These are ignored by frrcfgd but fool BGPConfigured() into thinking
	// BGP is already set up, causing the auto-ensure to skip BGP_GLOBALS.
	// Runs unconditionally — even if frrcfgd is already enabled, the legacy
	// entries may still be present from the factory config.
	removed, err := dev.RemoveLegacyBGPEntries(ctx)
	if err != nil {
		return fmt.Errorf("removing legacy BGP entries: %w", err)
	}

	// Check if already initialized via Node method (respects API boundary)
	if dev.IsUnifiedConfigMode() {
		if removed > 0 {
			if err := dev.SaveConfig(ctx); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
		}
		return ErrAlreadyInitialized
	}

	// Safety check: if the device has active BGP neighbors, initialization
	// will restart the bgp container and drop all sessions. This is
	// dangerous on a production device — require --force.
	if !force {
		configDB := dev.ConfigDB()
		if configDB != nil && len(configDB.BGPNeighbor) > 0 {
			return fmt.Errorf("%s has %d active BGP neighbor(s) — %w",
				device, len(configDB.BGPNeighbor), ErrActiveConfiguration)
		}
	}

	node := &Node{net: net, internal: dev}

	// Write unified config mode fields to DEVICE_METADATA and commit.
	// Fields from sonic.FrrcfgdMetadataFields() — single source of truth.
	_, err = node.Execute(ctx, ExecOpts{Execute: true, NoSave: true}, func(ctx context.Context) error {
		return node.SetDeviceMetadata(ctx, sonic.FrrcfgdMetadataFields())
	})
	if err != nil {
		return fmt.Errorf("writing DEVICE_METADATA: %w", err)
	}

	// Restart bgp so frrcfgd takes over from bgpcfgd
	if err := dev.EnsureUnifiedConfigMode(ctx); err != nil {
		return fmt.Errorf("enabling unified config mode: %w", err)
	}

	// Save config so unified mode persists across reboots
	if err := dev.SaveConfig(ctx); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}

// ListNodes returns the names of all devices that have been loaded into this Network.
func (net *Network) ListNodes() []string {
	return net.internal.ListNodes()
}

// HasTopology returns true if a topology.json was loaded with the spec files.
func (net *Network) HasTopology() bool {
	return net.internal.HasTopology()
}

// TopologyDeviceNames returns the sorted device names from the topology.
// Returns nil if no topology is loaded.
func (net *Network) TopologyDeviceNames() []string {
	topo := net.internal.GetTopology()
	if topo == nil {
		return nil
	}
	return topo.DeviceNames()
}

// IsHostDevice returns true if the named device is a virtual host (not a SONiC switch).
func (net *Network) IsHostDevice(name string) bool {
	return net.internal.IsHostDevice(name)
}

// GetHostProfile returns connection parameters for a host device.
func (net *Network) GetHostProfile(name string) (*HostProfile, error) {
	p, err := net.internal.GetHostProfile(name)
	if err != nil {
		return nil, err
	}
	return &HostProfile{
		MgmtIP:  p.MgmtIP,
		SSHUser: p.SSHUser,
		SSHPass: p.SSHPass,
		SSHPort: p.SSHPort,
	}, nil
}

// InitFromDeviceIntent creates a node whose projection is built from the device's
// own NEWTRON_INTENT records. The device's raw CONFIG_DB is never assigned to the
// node — the projection is derived entirely from intent replay.
//
// Architecture §3: actuated mode construction.
func (net *Network) InitFromDeviceIntent(ctx context.Context, device string) (*Node, error) {
	dev, err := net.internal.InitFromDeviceIntent(ctx, device)
	if err != nil {
		return nil, fmt.Errorf("initializing from device intents on %s: %w", device, err)
	}
	return &Node{net: net, internal: dev}, nil
}

// BuildTopologyNode creates a node whose projection is built from topology.json
// steps. The node is fully replayed but has no device connection. Used for
// topology-mode operations (tree, drift, reconcile, save, CRUD).
//
// Architecture §3: topology mode construction.
func (net *Network) BuildTopologyNode(device string) (*Node, error) {
	if !net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded — topology mode requires a topology"}
	}
	tp, err := network.NewTopologyProvisioner(net.internal)
	if err != nil {
		return nil, err
	}
	// Check if device has steps. After clear+save, topology.json may have
	// zero steps — build an empty node (ports only) instead of erroring.
	// Architecture §6 (Reload): "Destroys the current node and rebuilds
	// from topology.json." An empty topology.json entry is a valid state.
	topoDev, err := net.internal.GetTopologyDevice(device)
	if err != nil {
		return nil, err
	}
	if len(topoDev.Steps) == 0 {
		return net.BuildEmptyTopologyNode(device)
	}
	dev, err := tp.BuildAbstractNode(device)
	if err != nil {
		return nil, err
	}
	return &Node{net: net, internal: dev}, nil
}

// BuildEmptyTopologyNode creates a node with ports registered from topology.json
// but no intents replayed. Used for intent clear (empty canvas).
//
// Architecture §6 Clear.
func (net *Network) BuildEmptyTopologyNode(device string) (*Node, error) {
	if !net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded — topology mode requires a topology"}
	}
	tp, err := network.NewTopologyProvisioner(net.internal)
	if err != nil {
		return nil, err
	}
	dev, err := tp.BuildEmptyAbstractNode(device)
	if err != nil {
		return nil, err
	}
	return &Node{net: net, internal: dev}, nil
}

// SaveDeviceIntents persists the device's intent steps to topology.json.
// Called after Tree() to convert the node's intent DB into topology steps.
//
// Architecture §6 Save: "Update topology.json + persist."
func (net *Network) SaveDeviceIntents(device string, steps []spec.TopologyStep) error {
	if !net.HasTopology() {
		return &ValidationError{Message: "no topology loaded — save requires a topology"}
	}
	tp, err := network.NewTopologyProvisioner(net.internal)
	if err != nil {
		return err
	}
	return tp.SaveDeviceIntents(device, steps)
}

func (net *Network) checkPermission(perm auth.Permission, authCtx *auth.Context) error {
	if net.auth != nil {
		return net.auth.Check(perm, authCtx)
	}
	return nil
}
