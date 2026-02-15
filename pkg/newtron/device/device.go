package device

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// Device represents a SONiC switch.
type Device struct {
	Name     string
	Profile  *spec.ResolvedProfile
	ConfigDB *ConfigDB
	StateDB  *StateDB
	State    *DeviceState

	// v3: Platform configuration from device's platform.json
	PlatformConfig *SonicPlatformConfig

	// Redis connections
	client      *ConfigDBClient
	stateClient *StateDBClient
	applClient  *AppDBClient  // APP_DB (DB 0) for route verification
	asicClient  *AsicDBClient // ASIC_DB (DB 1) for ASIC-level verification
	tunnel      *SSHTunnel    // SSH tunnel for Redis access (nil if direct)
	connected   bool
	locked      bool
	lockHolder  string // holder identity for distributed lock

	// Mutex for thread safety
	mu sync.RWMutex
}

// DeviceState holds the current operational state of the device
type DeviceState struct {
	Interfaces   map[string]*InterfaceState
	PortChannels map[string]*PortChannelState
	VLANs        map[int]*VLANState
	VRFs         map[string]*VRFState
	BGP          *BGPState
	EVPN         *EVPNState
}

// InterfaceState represents interface operational state
type InterfaceState struct {
	Name        string
	AdminStatus string
	OperStatus  string
	Speed       string
	MTU         int
	VRF         string
	IPAddresses []string
	Service     string
	IngressACL  string
	EgressACL   string
	LAGMember   string // Parent LAG if member
}

// PortChannelState represents LAG operational state
type PortChannelState struct {
	Name          string
	AdminStatus   string
	OperStatus    string
	Members       []string
	ActiveMembers []string
}

// VLANState represents VLAN operational state
type VLANState struct {
	ID         int
	Name       string
	OperStatus string
	Members    []string
	SVIStatus  string
	L2VNI      int
}

// VRFState represents VRF operational state
type VRFState struct {
	Name       string
	State      string
	Interfaces []string
	L3VNI      int
	RouteCount int
}

// BGPState represents BGP operational state
type BGPState struct {
	LocalAS   int
	RouterID  string
	Neighbors map[string]*BGPNeighborState
}

// BGPNeighborState represents BGP neighbor state
type BGPNeighborState struct {
	Address  string
	RemoteAS int
	State    string
	PfxRcvd  int
	PfxSent  int
	Uptime   string
}

// EVPNState represents EVPN operational state
type EVPNState struct {
	VTEPState   string
	RemoteVTEPs []string
	VNICount    int
	Type2Routes int
	Type5Routes int
}

// NewDevice creates a new device instance
func NewDevice(name string, profile *spec.ResolvedProfile) *Device {
	return &Device{
		Name:    name,
		Profile: profile,
		State: &DeviceState{
			Interfaces:   make(map[string]*InterfaceState),
			PortChannels: make(map[string]*PortChannelState),
			VLANs:        make(map[int]*VLANState),
			VRFs:         make(map[string]*VRFState),
		},
	}
}

// Connect establishes connection to the device's config_db via Redis
func (d *Device) Connect(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.connected {
		return nil
	}

	var addr string
	if d.Profile.SSHUser != "" && d.Profile.SSHPass != "" {
		tun, err := NewSSHTunnel(d.Profile.MgmtIP, d.Profile.SSHUser, d.Profile.SSHPass, d.Profile.SSHPort)
		if err != nil {
			return fmt.Errorf("SSH tunnel to %s: %w", d.Name, err)
		}
		d.tunnel = tun
		addr = tun.LocalAddr()
	} else {
		addr = fmt.Sprintf("%s:6379", d.Profile.MgmtIP)
	}

	// Connect to CONFIG_DB (DB 4)
	d.client = NewConfigDBClient(addr)
	if err := d.client.Connect(); err != nil {
		return fmt.Errorf("connecting to config_db on %s: %w", d.Name, err)
	}

	// Load config_db
	var err error
	d.ConfigDB, err = d.client.GetAll()
	if err != nil {
		d.client.Close()
		return fmt.Errorf("loading config_db from %s: %w", d.Name, err)
	}

	// Connect to STATE_DB (DB 6)
	d.stateClient = NewStateDBClient(addr)
	if err := d.stateClient.Connect(); err != nil {
		// State DB connection failure is non-fatal - log warning and continue
		util.WithDevice(d.Name).Warnf("Failed to connect to state_db: %v", err)
	} else {
		d.StateDB, err = d.stateClient.GetAll()
		if err != nil {
			util.WithDevice(d.Name).Warnf("Failed to load state_db: %v", err)
		}
	}

	// Populate device state from state_db + config_db (works with nil StateDB)
	PopulateDeviceState(d.State, d.StateDB, d.ConfigDB)

	// Connect APP_DB (DB 0) for route verification — non-fatal
	d.applClient = NewAppDBClient(addr)
	if err := d.applClient.Connect(); err != nil {
		util.WithDevice(d.Name).Warnf("Failed to connect to app_db: %v", err)
		d.applClient = nil
	}

	// Connect ASIC_DB (DB 1) for ASIC-level verification — non-fatal
	// Expected to fail on VPP (no real ASIC); visible with -v
	d.asicClient = NewAsicDBClient(addr)
	if err := d.asicClient.Connect(); err != nil {
		util.WithDevice(d.Name).Debugf("Failed to connect to asic_db: %v", err)
		d.asicClient = nil
	}

	d.connected = true
	util.WithDevice(d.Name).Info("Connected")

	return nil
}

// Disconnect closes the connection
func (d *Device) Disconnect() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.connected {
		return nil
	}

	if d.locked {
		if err := d.unlock(); err != nil {
			util.WithDevice(d.Name).Warnf("Failed to release lock: %v", err)
		}
	}

	if d.client != nil {
		d.client.Close()
	}

	if d.stateClient != nil {
		d.stateClient.Close()
	}

	if d.applClient != nil {
		d.applClient.Close()
	}

	if d.asicClient != nil {
		d.asicClient.Close()
	}

	if d.tunnel != nil {
		d.tunnel.Close()
		d.tunnel = nil
	}

	d.connected = false
	util.WithDevice(d.Name).Info("Disconnected")

	return nil
}

// IsConnected returns true if connected to the device
func (d *Device) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.connected
}

// RequireConnected returns an error if not connected
func (d *Device) RequireConnected() error {
	if !d.IsConnected() {
		return util.NewPreconditionError("operation", d.Name, "device must be connected", "")
	}
	return nil
}

// RequireLocked returns an error if not locked
func (d *Device) RequireLocked() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return util.NewPreconditionError("operation", d.Name, "device must be connected", "")
	}
	if !d.locked {
		return util.NewPreconditionError("operation", d.Name, "device must be locked for changes", "use Lock() first")
	}
	return nil
}

// Lock acquires a distributed lock on the device via STATE_DB.
// The holder string identifies who holds the lock; ttlSeconds controls expiry.
func (d *Device) Lock(holder string, ttlSeconds int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.connected {
		return util.ErrNotConnected
	}

	if d.locked {
		return nil // Already locked
	}

	if d.stateClient != nil {
		if err := d.stateClient.AcquireLock(d.Name, holder, ttlSeconds); err != nil {
			return err
		}
	}

	d.locked = true
	d.lockHolder = holder
	util.WithDevice(d.Name).Debugf("Lock acquired by %s", holder)

	return nil
}

// Unlock releases the device lock via STATE_DB.
func (d *Device) Unlock() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.unlock()
}

func (d *Device) unlock() error {
	if !d.locked {
		return nil
	}

	if d.stateClient != nil && d.lockHolder != "" {
		if err := d.stateClient.ReleaseLock(d.Name, d.lockHolder); err != nil {
			util.WithDevice(d.Name).Warnf("Failed to release lock: %v", err)
		}
	}

	d.locked = false
	d.lockHolder = ""
	util.WithDevice(d.Name).Debug("Lock released")

	return nil
}

// LockHolder returns the current lock holder and acquisition time.
// Returns ("", zero, nil) if no lock is held.
func (d *Device) LockHolder() (string, time.Time, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.stateClient == nil {
		return "", time.Time{}, fmt.Errorf("state_db client not connected")
	}
	return d.stateClient.GetLockHolder(d.Name)
}

// IsLocked returns true if the device is locked
func (d *Device) IsLocked() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.locked
}

// Reload reloads the config_db and state_db from the device
func (d *Device) Reload(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.connected {
		return util.ErrNotConnected
	}

	var err error
	d.ConfigDB, err = d.client.GetAll()
	if err != nil {
		return fmt.Errorf("reloading config_db: %w", err)
	}

	// Reload state_db if connected
	if d.stateClient != nil {
		d.StateDB, err = d.stateClient.GetAll()
		if err != nil {
			util.WithDevice(d.Name).Warnf("Failed to reload state_db: %v", err)
		}
	}

	// Re-populate device state (works with nil StateDB)
	PopulateDeviceState(d.State, d.StateDB, d.ConfigDB)

	return nil
}

// GetInterface returns interface state by name.
func (d *Device) GetInterface(name string) (*InterfaceState, error) {
	if err := d.RequireConnected(); err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	intf, ok := d.State.Interfaces[name]
	if !ok {
		return nil, fmt.Errorf("interface %s not found", name)
	}
	return intf, nil
}

// GetPortChannel returns LAG state by name.
func (d *Device) GetPortChannel(name string) (*PortChannelState, error) {
	if err := d.RequireConnected(); err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	pc, ok := d.State.PortChannels[name]
	if !ok {
		return nil, fmt.Errorf("port channel %s not found", name)
	}
	return pc, nil
}

// GetVLAN returns VLAN state by ID
func (d *Device) GetVLAN(id int) (*VLANState, error) {
	if err := d.RequireConnected(); err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	vlan, ok := d.State.VLANs[id]
	if !ok {
		return nil, fmt.Errorf("VLAN %d not found", id)
	}
	return vlan, nil
}

// GetVRF returns VRF state by name
func (d *Device) GetVRF(name string) (*VRFState, error) {
	if err := d.RequireConnected(); err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	vrf, ok := d.State.VRFs[name]
	if !ok {
		return nil, fmt.Errorf("VRF %s not found", name)
	}
	return vrf, nil
}

// InterfaceHasService checks if an interface has a service bound
func (d *Device) InterfaceHasService(name string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if intf, ok := d.State.Interfaces[name]; ok {
		return intf.Service != ""
	}
	return false
}

// InterfaceIsLAGMember checks if an interface is a LAG member.
func (d *Device) InterfaceIsLAGMember(name string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for key := range d.ConfigDB.PortChannelMember {
		// Key format: PortChannel100|Ethernet0
		parts := splitKey(key)
		if len(parts) == 2 && parts[1] == name {
			return true
		}
	}
	return false
}

// GetInterfaceLAG returns the LAG that an interface belongs to.
func (d *Device) GetInterfaceLAG(name string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for key := range d.ConfigDB.PortChannelMember {
		if len(key) > len(name)+1 {
			parts := splitKey(key)
			if len(parts) == 2 && parts[1] == name {
				return parts[0]
			}
		}
	}
	return ""
}

func splitKey(key string) []string {
	for i := range key {
		if key[i] == '|' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}

// Client returns the underlying ConfigDB client for direct access
func (d *Device) Client() *ConfigDBClient {
	return d.client
}

// ApplyChanges writes a set of changes to config_db via Redis.
// This is a pure write — it does not reload the CONFIG_DB cache afterward.
// Cache refresh is the caller's responsibility via Lock() (for write episodes)
// or Refresh() (for read-only episodes). See HLD §4.10 for the episode model.
func (d *Device) ApplyChanges(changes []ConfigChange) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.connected {
		return util.ErrNotConnected
	}
	if !d.locked {
		return fmt.Errorf("device must be locked for changes")
	}

	for _, change := range changes {
		var err error
		switch change.Type {
		case ChangeTypeAdd, ChangeTypeModify:
			err = d.client.Set(change.Table, change.Key, change.Fields)
		case ChangeTypeDelete:
			err = d.client.Delete(change.Table, change.Key)
		}
		if err != nil {
			return fmt.Errorf("applying change to %s|%s: %w", change.Table, change.Key, err)
		}
	}

	return nil
}

// ConfigChange represents a single configuration change
type ConfigChange struct {
	Table  string
	Key    string
	Type   ChangeType
	Fields map[string]string
}

// ChangeType represents the type of configuration change
type ChangeType string

const (
	ChangeTypeAdd    ChangeType = "add"
	ChangeTypeModify ChangeType = "modify"
	ChangeTypeDelete ChangeType = "delete"
)

// StateClient returns the underlying StateDB client for direct access
func (d *Device) StateClient() *StateDBClient {
	return d.stateClient
}

// GetInterfaceOperState returns the operational state of an interface.
func (d *Device) GetInterfaceOperState(name string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.StateDB == nil {
		return "", fmt.Errorf("state_db not loaded")
	}

	if state, ok := d.StateDB.PortTable[name]; ok {
		return state.OperStatus, nil
	}
	return "", fmt.Errorf("interface %s not found in state_db", name)
}

// GetBGPNeighborOperState returns the operational state of a BGP neighbor
func (d *Device) GetBGPNeighborOperState(neighbor string) (*BGPNeighborState, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.State == nil || d.State.BGP == nil {
		return nil, fmt.Errorf("BGP state not loaded")
	}

	if state, ok := d.State.BGP.Neighbors[neighbor]; ok {
		return state, nil
	}
	return nil, fmt.Errorf("BGP neighbor %s not found", neighbor)
}

// GetPortChannelOperState returns the operational state of a PortChannel
func (d *Device) GetPortChannelOperState(name string) (*PortChannelState, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.State == nil {
		return nil, fmt.Errorf("state not loaded")
	}

	if state, ok := d.State.PortChannels[name]; ok {
		return state, nil
	}
	return nil, fmt.Errorf("PortChannel %s not found", name)
}

// GetVRFOperState returns the operational state of a VRF
func (d *Device) GetVRFOperState(name string) (*VRFState, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.State == nil {
		return nil, fmt.Errorf("state not loaded")
	}

	if state, ok := d.State.VRFs[name]; ok {
		return state, nil
	}
	return nil, fmt.Errorf("VRF %s not found", name)
}

// GetEVPNState returns the EVPN operational state
func (d *Device) GetEVPNState() *EVPNState {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.State == nil {
		return nil
	}
	return d.State.EVPN
}

// HasStateDB returns true if state_db is available
func (d *Device) HasStateDB() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.StateDB != nil
}

// ReloadConfig triggers a config reload on the SONiC device by executing
// `sudo config reload -y` via SSH. This causes SONiC to re-read CONFIG_DB
// and apply any changes (e.g., new BGP neighbors added by frrcfgd).
// Requires an active SSH tunnel (SSHUser/SSHPass in profile).
func (d *Device) ReloadConfig(ctx context.Context) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return fmt.Errorf("device not connected")
	}
	if d.tunnel == nil {
		return fmt.Errorf("config reload requires SSH connection (no SSH credentials configured)")
	}

	output, err := d.tunnel.ExecCommand("sudo config reload -y")
	if err != nil {
		return fmt.Errorf("config reload failed: %w (output: %s)", err, output)
	}
	return nil
}

// SaveConfig persists the running CONFIG_DB to disk by executing `sudo config save -y`
// on the SONiC device via SSH. Requires an active SSH tunnel (SSHUser/SSHPass in profile).
// Returns error if no SSH tunnel is available (direct Redis connection).
func (d *Device) SaveConfig(ctx context.Context) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return fmt.Errorf("device not connected")
	}
	if d.tunnel == nil {
		return fmt.Errorf("config save requires SSH connection (no SSH credentials configured)")
	}

	output, err := d.tunnel.ExecCommand("sudo config save -y")
	if err != nil {
		return fmt.Errorf("config save failed: %w (output: %s)", err, output)
	}
	return nil
}

// GetRoute reads a route from APP_DB (Redis DB 0) via the AppDBClient.
// Parses the comma-separated nexthop/ifname fields into []NextHop.
// Returns nil RouteEntry (not error) if the prefix is not present.
// Single-shot read — does not poll or retry.
func (d *Device) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return nil, fmt.Errorf("device not connected")
	}
	if d.applClient == nil {
		return nil, fmt.Errorf("APP_DB client not connected on %s", d.Name)
	}
	return d.applClient.GetRoute(vrf, prefix)
}

// GetRouteASIC reads a route from ASIC_DB (Redis DB 1) by resolving the SAI
// object chain: SAI_ROUTE_ENTRY -> SAI_NEXT_HOP_GROUP -> SAI_NEXT_HOP.
// Returns nil RouteEntry (not error) if not programmed in ASIC.
// Single-shot read — does not poll or retry.
func (d *Device) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return nil, fmt.Errorf("device not connected")
	}
	if d.asicClient == nil {
		return nil, fmt.Errorf("ASIC_DB client not connected on %s", d.Name)
	}
	return d.asicClient.GetRouteASIC(vrf, prefix, d.ConfigDB)
}

// VerifyChangeSet re-reads CONFIG_DB via a fresh connection and compares against
// the ChangeSet to confirm that writes were persisted.
//
// For ChangeAdd/ChangeModify: asserts every field in NewValue is present with the
// same value (superset match — Redis may have additional fields).
// For ChangeDelete: asserts the key does not exist in Redis.
//
// Uses a fresh ConfigDBClient on the existing SSH tunnel to avoid reading from
// the cached d.ConfigDB that was updated by Apply().
func (d *Device) VerifyChangeSet(ctx context.Context, changes []ConfigChange) (*VerificationResult, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return nil, fmt.Errorf("device not connected")
	}

	// Determine the address for the fresh connection
	var addr string
	if d.tunnel != nil {
		addr = d.tunnel.LocalAddr()
	} else {
		addr = fmt.Sprintf("%s:6379", d.Profile.MgmtIP)
	}

	// Create a fresh CONFIG_DB client for independent verification
	freshClient := NewConfigDBClient(addr)
	if err := freshClient.Connect(); err != nil {
		return nil, fmt.Errorf("fresh config_db connection: %w", err)
	}
	defer freshClient.Close()

	result := &VerificationResult{}

	for _, change := range changes {
		switch change.Type {
		case ChangeTypeAdd, ChangeTypeModify:
			// Read the table/key from fresh Redis and verify fields
			actual, err := freshClient.Get(change.Table, change.Key)
			if err != nil {
				return nil, fmt.Errorf("reading %s|%s: %w", change.Table, change.Key, err)
			}
			if len(actual) == 0 {
				result.Failed++
				result.Errors = append(result.Errors, VerificationError{
					Table:    change.Table,
					Key:      change.Key,
					Field:    "(all)",
					Expected: "present",
					Actual:   "",
				})
				continue
			}
			// Check each expected field (superset match)
			allMatch := true
			for field, expected := range change.Fields {
				if got, ok := actual[field]; !ok || got != expected {
					result.Failed++
					allMatch = false
					actualVal := ""
					if ok {
						actualVal = got
					}
					result.Errors = append(result.Errors, VerificationError{
						Table:    change.Table,
						Key:      change.Key,
						Field:    field,
						Expected: expected,
						Actual:   actualVal,
					})
				}
			}
			if allMatch {
				result.Passed++
			}
		case ChangeTypeDelete:
			exists, err := freshClient.Exists(change.Table, change.Key)
			if err != nil {
				return nil, fmt.Errorf("checking %s|%s: %w", change.Table, change.Key, err)
			}
			if exists {
				result.Failed++
				result.Errors = append(result.Errors, VerificationError{
					Table:    change.Table,
					Key:      change.Key,
					Field:    "(all)",
					Expected: "deleted",
					Actual:   "present",
				})
			} else {
				result.Passed++
			}
		}
	}

	return result, nil
}

// ApplyFRRDefaults sets FRR runtime defaults that the frrcfgd template does not
// support via CONFIG_DB. Currently handles:
//   - no bgp ebgp-requires-policy (FRR default: enabled)
//   - no bgp suppress-fib-pending (FRR default: enabled)
//   - neighbor X ttl-security hops 10 + disable-connected-check
//     (for CONFIG_DB neighbors with ebgp_multihop: true)
//
// We use ttl-security instead of ebgp-multihop because FRR silently ignores
// ebgp-multihop when local-as == remote-as (classifies as iBGP for that check),
// while still using eBGP TTL=1 on the socket. ttl-security sets outgoing
// TTL=255 regardless of eBGP/iBGP classification.
//
// Must be called after a BGP container restart since frr.conf is regenerated.
func (d *Device) ApplyFRRDefaults(ctx context.Context) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return fmt.Errorf("device not connected")
	}
	if d.tunnel == nil {
		return fmt.Errorf("ApplyFRRDefaults requires SSH connection")
	}

	// Read bgp_asn from CONFIG_DB to build the correct vtysh command
	asn := ""
	if d.ConfigDB != nil {
		if meta, ok := d.ConfigDB.DeviceMetadata["localhost"]; ok {
			asn = meta["bgp_asn"]
		}
	}
	if asn == "" {
		return fmt.Errorf("cannot determine BGP ASN from CONFIG_DB")
	}

	// Build vtysh commands: global defaults + per-neighbor TTL security
	cmds := fmt.Sprintf(
		"vtysh -c 'configure terminal' -c 'router bgp %s' "+
			"-c 'no bgp ebgp-requires-policy' "+
			"-c 'no bgp suppress-fib-pending'",
		asn)

	// Add ttl-security + disable-connected-check for overlay neighbors that
	// need multi-hop support. frrcfgd doesn't render ebgp_multihop, and FRR
	// silently ignores the ebgp-multihop command for peers with local-as ==
	// remote-as. ttl-security works regardless of FRR's eBGP/iBGP classification.
	if d.ConfigDB != nil {
		for key, neighbor := range d.ConfigDB.BGPNeighbor {
			if neighbor.EBGPMultihop == "true" {
				// Key format: "default|10.0.0.1" — extract the IP
				parts := strings.SplitN(key, "|", 2)
				if len(parts) == 2 {
					cmds += fmt.Sprintf(
						" -c 'neighbor %s ttl-security hops 10'"+
							" -c 'neighbor %s disable-connected-check'",
						parts[1], parts[1])
				}
			}
		}
	}

	cmds += " -c 'end' -c 'write memory'"

	output, err := d.tunnel.ExecCommand(cmds)
	if err != nil {
		return fmt.Errorf("ApplyFRRDefaults failed: %w (output: %s)", err, output)
	}

	// Force route reprocessing after changing defaults. "soft" (without
	// direction) clears both inbound and outbound: outbound re-advertises
	// local routes, inbound reprocesses received routes that may have been
	// suppressed while suppress-fib-pending was still active on this device.
	_, _ = d.tunnel.ExecCommand("vtysh -c 'clear bgp * soft'")

	return nil
}

// RestartService restarts a SONiC Docker container by name (e.g., "bgp", "swss",
// "syncd") via SSH. Uses "docker restart" instead of "systemctl restart" because
// SONiC VPP's systemd unit has an ExecStopPost script (write_standby.py) that
// exits with error code 1 on non-Dual-ToR systems, causing systemctl to report
// failure even though the container restarted successfully.
func (d *Device) RestartService(ctx context.Context, name string) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return fmt.Errorf("device not connected")
	}
	if d.tunnel == nil {
		return fmt.Errorf("restart service requires SSH connection (no SSH credentials configured)")
	}

	output, err := d.tunnel.ExecCommand(fmt.Sprintf("sudo docker restart %s", name))
	if err != nil {
		return fmt.Errorf("restart service %s failed: %w (output: %s)", name, err, output)
	}
	return nil
}

// Tunnel returns the SSH tunnel for direct access (e.g., newtest SSH commands).
// Returns nil if no SSH tunnel is configured (direct Redis connection).
func (d *Device) Tunnel() *SSHTunnel {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tunnel
}
