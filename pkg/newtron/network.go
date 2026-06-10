package newtron

import (
	"context"
	"errors"
	"fmt"
	stdnet "net"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	netpkg "github.com/aldrin-isaac/newtron/pkg/newtron/network"
	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// Network is the top-level API entry point.
type Network struct {
	internal *netpkg.Network
	auth     *auth.Checker
}

// LoadNetwork loads all spec files from specDir and returns a Network ready for use.
//
// topologyName identifies this network to the injected port resolver
// (e.g., "1node-vs"). pr is consulted at Device.Connect time to
// resolve per-node SSH ports. Pass nil for tests and real-hardware
// deployments.
//
// secretStore (auth-design.md L0) is the operator-configured secret
// backend. When non-nil, ${secret:KEY} references in profile and
// platform values are resolved at load time. nil preserves the
// plaintext-only behavior — references in specs become hard errors
// at load.
func LoadNetwork(specDir, topologyName string, pr sonic.PortResolver, secretStore secret.Store) (*Network, error) {
	net, err := netpkg.NewNetwork(specDir, topologyName, pr, secretStore)
	if err != nil {
		return nil, err
	}
	return &Network{internal: net}, nil
}

// EnableAuthorization wires permission enforcement for this Network
// (auth-design.md L3). After it returns, every spec/profile
// mutation method's checkPermission call consults the network's
// permissions map — denials surface as auth.PermissionError, which
// pkg/newtron/api maps to HTTP 403.
//
// Disabled state (no call to EnableAuthorization) preserves pre-L3
// behavior: every checkPermission returns nil. This is the L3
// half of the §2.4 enable/disable contract — operators opt in via
// the --enforce-authorization flag.
//
// EnableAuthorization binds the checker to the spec snapshot live at
// call time. ReloadNetwork replaces the whole Network and so a fresh
// EnableAuthorization is required to re-bind the checker against the
// new spec. In-process spec mutations after EnableAuthorization
// (CreateService, DeleteProfile, …) are observed through the same
// spec pointer — no re-call needed for grant changes to take effect.
func (net *Network) EnableAuthorization() {
	net.auth = auth.NewChecker(net.internal.Spec())
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
	if err := net.checkPermission(ctx, auth.PermDeviceWrite, auth.NewContext().WithDevice(device)); err != nil {
		return err
	}
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

// GetTopology returns the full topology spec, or nil when no topology.json was
// loaded for this network. §46: canonical `spec.TopologySpecFile` substrate
// exposed directly, alongside the names-only summary in TopologyDeviceNames.
func (net *Network) GetTopology() *spec.TopologySpecFile {
	return net.internal.GetTopology()
}

// AddTopologyDevice adds a device entry to topology.json. Returns
// *ConflictError when a device with this name already exists. The matching
// profile file must already exist. Persists atomically. §7 + §15 + §27 + §46.
func (net *Network) AddTopologyDevice(ctx context.Context, name string, device *spec.TopologyDevice) error {
	if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("topology").WithDevice(name).WithResource(name)); err != nil {
		return err
	}
	return translateInternalError(net.internal.AddTopologyDevice(name, device))
}

// DeleteTopologyDevice removes a device entry from topology.json. Refuses with
// *ConflictError when any link still references the device, unless force=true.
// With force=true, cascade-deletes the referring links before removing the
// device. Persists atomically. §15 (cascade is explicit).
func (net *Network) DeleteTopologyDevice(ctx context.Context, name string, force bool) error {
	if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("topology").WithDevice(name).WithResource(name)); err != nil {
		return err
	}
	return translateInternalError(net.internal.DeleteTopologyDevice(name, force))
}

// UpdateTopologyDevice replaces the device entry at name with the given
// TopologyDevice (full-replacement semantics; no partial patch). Returns
// *NotFoundError when name doesn't exist.
func (net *Network) UpdateTopologyDevice(ctx context.Context, name string, device *spec.TopologyDevice) error {
	if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("topology").WithDevice(name).WithResource(name)); err != nil {
		return err
	}
	return translateInternalError(net.internal.UpdateTopologyDevice(name, device))
}

// AddTopologyLink adds a link to topology.json. Returns *ConflictError when
// either endpoint is already wired to another link (a port participates in
// at most one link). Validates that both endpoint devices exist in topology
// AND that each interface is declared on its device's Ports map.
func (net *Network) AddTopologyLink(ctx context.Context, link *spec.TopologyLink) error {
	if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("topology")); err != nil {
		return err
	}
	return translateInternalError(net.internal.AddTopologyLink(link))
}

// DeleteTopologyLink removes the link whose A or Z endpoint matches the given
// "device:interface" string. Single-endpoint identification per Q3 design:
// a port participates in at most one link, so one endpoint uniquely
// identifies the link. Returns *NotFoundError when no link contains the
// endpoint.
func (net *Network) DeleteTopologyLink(ctx context.Context, endpoint string) error {
	if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("topology").WithResource(endpoint)); err != nil {
		return err
	}
	return translateInternalError(net.internal.DeleteTopologyLink(endpoint))
}

// translateInternalError converts errors that surface from the internal
// network package's typed error vocab (which can't import this package to
// avoid a circular import) into the public newtron error vocab (NotFoundError
// and friends) callers expect. Pass-through for everything else.
func translateInternalError(err error) error {
	if err == nil {
		return nil
	}
	type notFounder interface {
		IsNotFound() bool
		Resource() string
		ID() string
	}
	if nf, ok := err.(notFounder); ok && nf.IsNotFound() {
		return &NotFoundError{Resource: nf.Resource(), Name: nf.ID()}
	}
	return err
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
// SSH port is runtime state owned by newtlab (§27) — resolved at
// request time through the configured PortResolver and included in
// the response so callers (newtrun's host-exec path, CLI) never need
// to know newtlab exists.
func (net *Network) GetHostProfile(ctx context.Context, name string) (*HostProfile, error) {
	p, err := net.internal.GetHostProfile(name)
	if err != nil {
		return nil, err
	}
	profile := &HostProfile{
		MgmtIP:  p.MgmtIP,
		SSHUser: p.SSHUser,
		SSHPass: p.SSHPass,
	}
	if resolver := net.internal.PortResolver(); resolver != nil {
		port, err := resolver.SSHPort(ctx, net.internal.TopologyName(), name)
		if err != nil {
			return nil, fmt.Errorf("resolving SSH port for host %q: %w", name, err)
		}
		profile.SSHPort = port
	}
	return profile, nil
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
	tp, err := netpkg.NewTopologyProvisioner(net.internal)
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
	tp, err := netpkg.NewTopologyProvisioner(net.internal)
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
	tp, err := netpkg.NewTopologyProvisioner(net.internal)
	if err != nil {
		return err
	}
	return tp.SaveDeviceIntents(device, steps)
}

// OnlineReason enumerates why a device is or isn't reachable. Tied to the
// /node/{device}/status response field of the same name (issue #75A) so the
// browser UI can render distinct indicators without parsing free-form
// strings.
type OnlineReason string

const (
	OnlineReasonSSHPortResolved    OnlineReason = "ssh_port_resolved"
	OnlineReasonNewtlabNotRealised OnlineReason = "newtlab_not_realised"
	OnlineReasonPortClosed         OnlineReason = "port_closed"
	OnlineReasonUnreachable        OnlineReason = "unreachable"
	OnlineReasonNoResolver         OnlineReason = "no_resolver"
)

// ProbeOnline is the cheap reachability probe behind the /status endpoint:
// resolve the SSH port via the configured PortResolver (no SSH session),
// then TCP-dial it with a 500ms timeout.
//
// Returns only the boolean + classified reason — there is no out-of-band
// error class; every failure mode maps to a reason enum. Designed to be
// cheap enough for newtcon to poll one-per-device on a short timer without
// warming an SSH connection each time.
func (net *Network) ProbeOnline(ctx context.Context, device string) (bool, OnlineReason) {
	resolver := net.internal.PortResolver()
	if resolver == nil {
		// Real-hardware/test mode has no runtime to ask. Caller renders
		// "no_resolver" as "unknown" in the UI.
		return false, OnlineReasonNoResolver
	}
	port, err := resolver.SSHPort(ctx, net.internal.TopologyName(), device)
	if err != nil {
		// Dispatch on the sonic.NotReadyError marker interface so this
		// package stays decoupled from the resolver impl package (§33,
		// §34). newtlab/client's *NotInLabError implements it; other
		// resolver impls leave it unimplemented and fall through to
		// "unreachable."
		var notReady sonic.NotReadyError
		if errors.As(err, &notReady) {
			return false, OnlineReasonNewtlabNotRealised
		}
		return false, OnlineReasonUnreachable
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, dialErr := stdnet.DialTimeout("tcp", addr, 500*time.Millisecond)
	if dialErr != nil {
		return false, OnlineReasonPortClosed
	}
	_ = conn.Close()
	return true, OnlineReasonSSHPortResolved
}

// TopologyDrift answers "does the device CONFIG_DB diverge from
// topology.json right now?" — independent of the operator's in-flight edits.
// Builds a transient TopologyNode from topology.json, connects transport,
// runs Drift, and closes. Does NOT touch any cached NodeActor state.
//
// Strictly more expensive than /intent/drift (it opens a fresh SSH session
// per call). Callers invoke it on-demand from a "show topology drift" UI
// action, not from a polling badge.
func (net *Network) TopologyDrift(ctx context.Context, device string) ([]DriftEntry, error) {
	node, err := net.BuildTopologyNode(device)
	if err != nil {
		return nil, err
	}
	defer node.Close()
	// Connect transport so Drift can read the device CONFIG_DB.
	if err := node.internal.ConnectTransport(ctx); err != nil {
		return nil, fmt.Errorf("connecting to %s for topology drift: %w", device, err)
	}
	return node.Drift(ctx)
}

// checkPermission is the single gate every spec/profile mutation
// passes through (auth-design.md L3). Disabled state (auth == nil)
// preserves pre-L3 behavior — every check returns nil. Enabled state
// populates authCtx.Caller from the verified identity attached to
// ctx by the HTTP boundary (audit.CallerFromContext) and delegates
// to the Checker. The audit emission is per-decision (allow and
// deny) so reviewers can see every gate's verdict alongside the
// L1 request-level event.
func (net *Network) checkPermission(ctx context.Context, perm auth.Permission, authCtx *auth.Context) error {
	if net.auth == nil {
		return nil
	}
	source := audit.VerificationUnknown
	if caller := audit.CallerFromContext(ctx); caller != nil {
		authCtx.Caller = caller.Username
		source = caller.Source
	}
	err := net.auth.Check(perm, authCtx)
	audit.LogDecision(audit.Decision{
		Permission: string(perm),
		Caller:     authCtx.Caller,
		Source:     source,
		Device:     authCtx.Device,
		Service:    authCtx.Service,
		Interface:  authCtx.Interface,
		Resource:   authCtx.Resource,
		Field:      authCtx.Field,
		Error:      err,
	})
	if err == nil {
		return nil
	}
	// Surface as the public-API typed error so the HTTP boundary
	// can map to 403 and so a client receives Caller/Permission/
	// Resource on the wire (§46). The original *auth.PermissionError
	// remains in the chain via Unwrap, preserving existing
	// errors.Is(util.ErrPermissionDenied) compatibility.
	return &AuthorizationError{
		Caller:     authCtx.Caller,
		Permission: string(perm),
		Resource:   authCtx.Resource,
		inner:      err,
	}
}
