// topology.go implements topology-driven provisioning from topology.json specs.
//
// ProvisionDevice generates a complete CONFIG_DB offline and delivers it
// atomically via node.CompositeOverwrite (no device interrogation needed).
//
// Uses the Abstract Node pattern: creates an offline Node with a shadow ConfigDB,
// calls the same Node/Interface methods used in the online path, and exports the
// accumulated entries as a CompositeConfig. topology.json represents an abstract
// topology in which abstract nodes live — the same code path handles both offline
// provisioning and online operations.
//
// Topology steps are pre-computed, fully-resolved operations in the topology.json
// file. GenerateDeviceComposite registers ports, then replays steps against an
// abstract Node via node.ReplayStep. One vocabulary: topology steps use the same
// operation names as the API and intent records.
//
// VerifyDeviceHealth generates the expected CONFIG_DB from the topology (same
// as the provisioner), then compares against the live device. Operational state
// checks (BGP sessions, interface oper-up) complement the config intent check.
package network

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/util"
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

// GenerateDeviceComposite generates a node.CompositeConfig for a device without delivering it.
// Useful for inspection, serialization, or deferred delivery.
// Returns error for host devices (no SONiC CONFIG_DB).
//
// Creates an abstract Node with the device's profile and resolved specs, registers
// physical ports from topology.Ports, then replays each topology step via
// node.ReplayStep. The accumulated entries are exported as a CompositeConfig.
func (tp *TopologyProvisioner) GenerateDeviceComposite(deviceName string) (*node.CompositeConfig, error) {
	if tp.network.IsHostDevice(deviceName) {
		return nil, fmt.Errorf("device '%s' is a host — cannot generate SONiC composite", deviceName)
	}
	topoDev, _ := tp.network.GetTopologyDevice(deviceName)

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

	// Create abstract node with empty shadow ConfigDB.
	// Operations build desired state; BuildComposite exports it.
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

	// Export accumulated entries as CompositeConfig
	composite := n.BuildComposite()
	composite.Metadata.GeneratedBy = "topology-provisioner"
	composite.Metadata.Description = fmt.Sprintf("Full device provisioning from topology.json for %s", deviceName)

	return composite, nil
}

// ProvisionDevice generates a complete CONFIG_DB for the named device from the
// topology spec and delivers it atomically with node.CompositeOverwrite mode.
//
// Flow:
//  1. Generate composite offline from specs + topology
//  2. Connect to device
//  3. Config reload — reset CONFIG_DB to saved defaults (factory fields intact)
//  4. Deliver composite — ReplaceAll merges on top of the clean baseline
//
// The config reload ensures factory fields (mac, platform, hwsku) are always
// present and stale fields from any prior provisioning are cleared.
func (tp *TopologyProvisioner) ProvisionDevice(ctx context.Context, deviceName string) (*node.CompositeDeliveryResult, error) {
	// Generate the composite config offline
	composite, err := tp.GenerateDeviceComposite(deviceName)
	if err != nil {
		return nil, fmt.Errorf("generating composite: %w", err)
	}

	// Connect without frrcfgd check — the composite includes unified config
	// mode fields, and we restart bgp after delivery.
	dev, err := tp.network.ConnectNodeForSetup(ctx, deviceName)
	if err != nil {
		return nil, fmt.Errorf("connecting to device: %w", err)
	}
	defer dev.Disconnect()

	// Best-effort config reload to restore CONFIG_DB to saved defaults.
	// On fresh boot: services may still be starting (SwSS not ready), and
	// CONFIG_DB is already in factory state — failure is expected and safe.
	// On re-provision: services are running, reload succeeds, giving us a
	// clean baseline with factory fields (mac, platform, hwsku) intact.
	util.WithDevice(deviceName).Info("Reloading config to restore defaults before provisioning")
	if err := dev.ConfigReload(ctx); err != nil {
		util.WithDevice(deviceName).Warnf("Config reload before provision skipped: %v (CONFIG_DB may already be in factory state)", err)
	} else {
		if err := dev.RefreshWithRetry(ctx, 60*time.Second); err != nil {
			return nil, fmt.Errorf("waiting for CONFIG_DB after reload: %w", err)
		}
	}

	// Lock for writing
	if err := dev.Lock(); err != nil {
		return nil, fmt.Errorf("locking device: %w", err)
	}
	defer dev.Unlock()

	// Deliver composite — ReplaceAll merges our fields on top of the
	// freshly-reloaded CONFIG_DB. Factory fields survive; stale keys
	// (present in DB but absent from composite) are removed.
	result, err := dev.DeliverComposite(composite, node.CompositeOverwrite)
	if err != nil {
		return nil, fmt.Errorf("delivering composite: %w", err)
	}

	// Ensure unified config mode is active. The composite wrote the frrcfgd
	// fields to DEVICE_METADATA. If the device was using bgpcfgd (community
	// sonic-vs default), restart bgp so frrcfgd takes over. No-op if already running.
	if err := dev.EnsureUnifiedConfigMode(ctx); err != nil {
		return nil, fmt.Errorf("enabling unified config mode: %w", err)
	}

	util.WithDevice(deviceName).Infof("Provisioned device from topology: %d entries applied", result.Applied)
	return result, nil
}

// ============================================================================
// Intent-based health verification
// ============================================================================

// DriftReport describes the differences between expected and actual CONFIG_DB
// for a single device. Used by DetectDrift.
type DriftReport struct {
	Device   string             `json:"device"`
	Status   string             `json:"status"` // "clean" or "drifted"
	Missing  []sonic.DriftEntry `json:"missing,omitempty"`
	Extra    []sonic.DriftEntry `json:"extra,omitempty"`
	Modified []sonic.DriftEntry `json:"modified,omitempty"`
}

// HealthReport combines config intent verification with operational state checks.
type HealthReport struct {
	Device      string                   `json:"device"`
	Status      string                   `json:"status"` // "pass", "warn", "fail"
	ConfigCheck *sonic.VerificationResult `json:"config_check"`
	OperChecks  []node.HealthCheckResult  `json:"oper_checks"`
}

// VerifyDeviceHealth generates expected CONFIG_DB entries from the topology,
// compares them against the live device, and checks operational state.
//
// Steps:
//  1. GenerateDeviceComposite → expected CONFIG_DB entries
//  2. ToConfigChanges + VerifyChangeSet → config intent verification
//  3. CheckBGPSessions → BGP operational state
//  4. CheckInterfaceOper → wired interface oper-up
//  5. Combine into HealthReport; overall status = worst of all checks
func (tp *TopologyProvisioner) VerifyDeviceHealth(ctx context.Context, deviceName string) (*HealthReport, error) {
	// Step 1: Generate expected CONFIG_DB entries from topology
	composite, err := tp.GenerateDeviceComposite(deviceName)
	if err != nil {
		return nil, fmt.Errorf("generating expected config: %w", err)
	}

	// Get the connected node
	dev, err := tp.network.GetNode(deviceName)
	if err != nil {
		return nil, fmt.Errorf("getting device: %w", err)
	}

	// Step 2: Verify composite against live CONFIG_DB
	configResult, err := dev.VerifyComposite(ctx, composite)
	if err != nil {
		return nil, fmt.Errorf("verifying config intent: %w", err)
	}

	// Step 3: Check BGP operational state
	bgpResults, err := dev.CheckBGPSessions(ctx)
	if err != nil {
		bgpResults = []node.HealthCheckResult{{
			Check: "bgp", Status: "fail",
			Message: fmt.Sprintf("BGP check error: %s", err),
		}}
	}

	// Step 4: Check interface oper-up for wired interfaces
	wiredInterfaces := tp.getWiredInterfaces(deviceName)
	var intfResults []node.HealthCheckResult
	if len(wiredInterfaces) > 0 {
		intfResults = dev.CheckInterfaceOper(wiredInterfaces)
	}

	// Combine oper checks
	var operChecks []node.HealthCheckResult
	operChecks = append(operChecks, bgpResults...)
	operChecks = append(operChecks, intfResults...)

	// Step 5: Derive overall status (worst of config + oper)
	report := &HealthReport{
		Device:      deviceName,
		ConfigCheck: configResult,
		OperChecks:  operChecks,
	}

	report.Status = "pass"
	if configResult.Failed > 0 {
		report.Status = "fail"
	}
	for _, oc := range operChecks {
		if oc.Status == "fail" {
			report.Status = "fail"
			break
		}
		if oc.Status == "warn" && report.Status == "pass" {
			report.Status = "warn"
		}
	}

	return report, nil
}

// DetectDrift compares expected CONFIG_DB (from topology + specs) against
// actual CONFIG_DB on the device. Returns drift entries for newtron-owned tables.
//
// The expected state comes from GenerateDeviceComposite() — the same code
// path that provisioning uses. The actual state comes from live CONFIG_DB.
// Tables outside newtron's ownership map are excluded.
func (tp *TopologyProvisioner) DetectDrift(ctx context.Context, deviceName string) (*DriftReport, error) {
	// Step 1: Generate expected CONFIG_DB from topology + specs
	composite, err := tp.GenerateDeviceComposite(deviceName)
	if err != nil {
		return nil, fmt.Errorf("generating expected config: %w", err)
	}

	// Step 2: Get the connected node and read actual CONFIG_DB
	dev, err := tp.network.GetNode(deviceName)
	if err != nil {
		return nil, fmt.Errorf("getting device: %w", err)
	}

	configClient := dev.ConfigDBClient()
	if configClient == nil {
		return nil, fmt.Errorf("no CONFIG_DB client for %s", deviceName)
	}

	actual, err := configClient.GetRawOwnedTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading actual CONFIG_DB: %w", err)
	}

	// Step 3: Convert composite tables to RawConfigDB format
	expected := sonic.RawConfigDB(composite.Tables)

	// Step 4: Diff
	ownedTables := sonic.OwnedTables()
	diffs := sonic.DiffConfigDB(expected, actual, ownedTables)

	// Build report
	report := &DriftReport{
		Device: deviceName,
		Status: "clean",
	}
	for _, d := range diffs {
		switch d.Type {
		case "missing":
			report.Missing = append(report.Missing, d)
		case "extra":
			report.Extra = append(report.Extra, d)
		case "modified":
			report.Modified = append(report.Modified, d)
		}
	}
	if len(report.Missing) > 0 || len(report.Extra) > 0 || len(report.Modified) > 0 {
		report.Status = "drifted"
	}

	return report, nil
}

// getWiredInterfaces returns the sorted list of Ethernet interfaces that have
// ports registered in the topology (i.e., connected to something).
func (tp *TopologyProvisioner) getWiredInterfaces(deviceName string) []string {
	topoDev, err := tp.network.GetTopologyDevice(deviceName)
	if err != nil {
		return nil
	}
	var interfaces []string
	for portName := range topoDev.Ports {
		if strings.HasPrefix(portName, "Ethernet") || strings.HasPrefix(portName, "PortChannel") {
			interfaces = append(interfaces, portName)
		}
	}
	sort.Strings(interfaces)
	return interfaces
}
