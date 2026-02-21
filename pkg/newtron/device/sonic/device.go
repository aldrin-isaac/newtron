package sonic

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/newtron-network/newtron/pkg/newtron/device"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// Device represents a SONiC switch.
type Device struct {
	Name     string
	Profile  *spec.ResolvedProfile
	ConfigDB *ConfigDB
	StateDB  *StateDB
	State    *device.DeviceState

	// Redis connections
	client      *ConfigDBClient
	stateClient *StateDBClient
	applClient  *AppDBClient  // APP_DB (DB 0) for route verification
	asicClient  *AsicDBClient // ASIC_DB (DB 1) for ASIC-level verification
	tunnel      *device.SSHTunnel    // SSH tunnel for Redis access (nil if direct)
	connected   bool
	locked      bool
	lockHolder  string // holder identity for distributed lock

	// Mutex for thread safety
	mu sync.RWMutex
}

// NewDevice creates a new device instance
func NewDevice(name string, profile *spec.ResolvedProfile) *Device {
	return &Device{
		Name:    name,
		Profile: profile,
		State: &device.DeviceState{
			Interfaces:   make(map[string]*device.InterfaceState),
			PortChannels: make(map[string]*device.PortChannelState),
			VLANs:        make(map[int]*device.VLANState),
			VRFs:         make(map[string]*device.VRFState),
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
		tun, err := device.NewSSHTunnel(d.Profile.MgmtIP, d.Profile.SSHUser, d.Profile.SSHPass, d.Profile.SSHPort)
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

// IsLocked returns true if the device is locked
func (d *Device) IsLocked() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.locked
}

// Client returns the underlying ConfigDB client for direct access
func (d *Device) Client() *ConfigDBClient {
	return d.client
}

// ApplyChanges writes a set of changes to config_db via Redis.
// This is a pure write — it does not reload the CONFIG_DB cache afterward.
// Cache refresh is the caller's responsibility via Lock() (for write episodes)
// or Refresh() (for read-only episodes). See HLD §4.10 for the episode model.
func (d *Device) ApplyChanges(changes []device.ConfigChange) error {
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
		case device.ChangeTypeAdd, device.ChangeTypeModify:
			err = d.client.Set(change.Table, change.Key, change.Fields)
		case device.ChangeTypeDelete:
			err = d.client.Delete(change.Table, change.Key)
		}
		if err != nil {
			return fmt.Errorf("applying change to %s|%s: %w", change.Table, change.Key, err)
		}
	}

	return nil
}


// StateClient returns the underlying StateDB client for direct access
func (d *Device) StateClient() *StateDBClient {
	return d.stateClient
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
// Parses the comma-separated nexthop/ifname fields into []device.NextHop.
// Returns nil device.RouteEntry (not error) if the prefix is not present.
// Single-shot read — does not poll or retry.
func (d *Device) GetRoute(ctx context.Context, vrf, prefix string) (*device.RouteEntry, error) {
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
// Returns nil device.RouteEntry (not error) if not programmed in ASIC.
// Single-shot read — does not poll or retry.
func (d *Device) GetRouteASIC(ctx context.Context, vrf, prefix string) (*device.RouteEntry, error) {
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
func (d *Device) VerifyChangeSet(ctx context.Context, changes []device.ConfigChange) (*device.VerificationResult, error) {
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

	result := &device.VerificationResult{}

	for _, change := range changes {
		switch change.Type {
		case device.ChangeTypeAdd, device.ChangeTypeModify:
			// Read the table/key from fresh Redis and verify fields
			actual, err := freshClient.Get(change.Table, change.Key)
			if err != nil {
				return nil, fmt.Errorf("reading %s|%s: %w", change.Table, change.Key, err)
			}
			if len(actual) == 0 {
				result.Failed++
				result.Errors = append(result.Errors, device.VerificationError{
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
					result.Errors = append(result.Errors, device.VerificationError{
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
		case device.ChangeTypeDelete:
			exists, err := freshClient.Exists(change.Table, change.Key)
			if err != nil {
				return nil, fmt.Errorf("checking %s|%s: %w", change.Table, change.Key, err)
			}
			if exists {
				result.Failed++
				result.Errors = append(result.Errors, device.VerificationError{
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
//
// Note: ebgp-multihop is now correctly rendered by frrcfgd from CONFIG_DB
// (BGP_NEIGHBOR.ebgp_multihop) and works as expected for eBGP sessions.
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

	// Read BGP ASN from CONFIG_DB — check DEVICE_METADATA first (provision path),
	// then fall back to BGP_GLOBALS.local_asn (configure-bgp path).
	asn := ""
	if d.ConfigDB != nil {
		if meta, ok := d.ConfigDB.DeviceMetadata["localhost"]; ok {
			asn = meta["bgp_asn"]
		}
		if asn == "" {
			if globals, ok := d.ConfigDB.BGPGlobals["default"]; ok {
				asn = globals.LocalASN
			}
		}
	}
	if asn == "" {
		return fmt.Errorf("cannot determine BGP ASN from CONFIG_DB")
	}

	// Build vtysh commands for global BGP defaults
	cmds := fmt.Sprintf(
		"vtysh -c 'configure terminal' -c 'router bgp %s' "+
			"-c 'no bgp ebgp-requires-policy' "+
			"-c 'no bgp suppress-fib-pending' "+
			"-c 'end' -c 'write memory'",
		asn)

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
// "syncd") via SSH. Uses "systemctl restart" to integrate properly with systemd.
func (d *Device) RestartService(ctx context.Context, name string) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.connected {
		return fmt.Errorf("device not connected")
	}
	if d.tunnel == nil {
		return fmt.Errorf("restart service requires SSH connection (no SSH credentials configured)")
	}

	output, err := d.tunnel.ExecCommand(fmt.Sprintf("sudo systemctl restart %s", name))
	if err != nil {
		return fmt.Errorf("restart service %s failed: %w (output: %s)", name, err, output)
	}
	return nil
}

// Tunnel returns the SSH tunnel for direct access (e.g., newtest SSH commands).
// Returns nil if no SSH tunnel is configured (direct Redis connection).
func (d *Device) Tunnel() *device.SSHTunnel {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tunnel
}

// ReadSystemMAC reads the system MAC address from the device's factory config file.
// Returns an empty string if the MAC cannot be read.
//
// Inherent: the system MAC is set by platform initialization at first boot and stored in
// /etc/sonic/config_db.json. vlanmgrd requires DEVICE_METADATA.localhost.mac to create
// VLAN bridge interfaces. The CompositeOverwrite provisioner must re-inject this MAC
// because it replaces the entire DEVICE_METADATA table.
func (d *Device) ReadSystemMAC() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.tunnel == nil {
		return ""
	}

	cmd := `python3 -c 'import json; d=json.load(open("/etc/sonic/config_db.json")); print(d.get("DEVICE_METADATA",{}).get("localhost",{}).get("mac",""))' 2>/dev/null`
	output, err := d.tunnel.ExecCommand("sudo " + cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}
