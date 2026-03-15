package newtron

import (
	"context"
	"errors"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/network"
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

// Connect loads the named device and establishes a connection to its Redis databases.
func (net *Network) Connect(ctx context.Context, device string) (*Node, error) {
	dev, err := net.internal.ConnectNode(ctx, device)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", device, err)
	}
	return &Node{net: net, internal: dev}, nil
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

// DetectDrift compares expected CONFIG_DB (from topology) against actual CONFIG_DB
// on a device. Returns a DriftReport describing any differences in newtron-owned tables.
// Requires a topology to be loaded and the device to be connected.
func (net *Network) DetectDrift(ctx context.Context, device string) (*DriftReport, error) {
	if !net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded — drift detection requires a topology"}
	}
	tp, err := network.NewTopologyProvisioner(net.internal)
	if err != nil {
		return nil, err
	}
	report, err := tp.DetectDrift(ctx, device)
	if err != nil {
		return nil, err
	}
	// Convert internal DriftReport to public type
	pub := &DriftReport{
		Device: report.Device,
		Status: report.Status,
	}
	for _, d := range report.Missing {
		pub.Missing = append(pub.Missing, DriftEntry{
			Table: d.Table, Key: d.Key, Type: d.Type,
			Expected: d.Expected,
		})
	}
	for _, d := range report.Extra {
		pub.Extra = append(pub.Extra, DriftEntry{
			Table: d.Table, Key: d.Key, Type: d.Type,
			Actual: d.Actual,
		})
	}
	for _, d := range report.Modified {
		pub.Modified = append(pub.Modified, DriftEntry{
			Table: d.Table, Key: d.Key, Type: d.Type,
			Expected: d.Expected, Actual: d.Actual,
		})
	}
	return pub, nil
}

// NetworkDrift runs drift detection across all topology devices and returns
// a summary. Each device is checked independently; errors on one device
// don't prevent checking others.
func (net *Network) NetworkDrift(ctx context.Context) (*NetworkDriftSummary, error) {
	if !net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded — drift detection requires a topology"}
	}
	devices := net.TopologyDeviceNames()
	summary := &NetworkDriftSummary{}
	for _, name := range devices {
		if net.IsHostDevice(name) {
			continue
		}
		report, err := net.DetectDrift(ctx, name)
		status := DeviceDriftStatus{Device: name}
		if err != nil {
			status.Error = err.Error()
			status.Status = "error"
		} else {
			status.Status = report.Status
			status.Missing = len(report.Missing)
			status.Extra = len(report.Extra)
			status.Modified = len(report.Modified)
		}
		summary.Devices = append(summary.Devices, status)
	}
	return summary, nil
}

func (net *Network) checkPermission(perm auth.Permission, authCtx *auth.Context) error {
	if net.auth != nil {
		return net.auth.Check(perm, authCtx)
	}
	return nil
}
