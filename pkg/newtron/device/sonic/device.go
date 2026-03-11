package sonic

import (
	"context"
	"fmt"
	"sync"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// Device represents a SONiC switch.
type Device struct {
	Name     string
	Profile  *spec.ResolvedProfile
	ConfigDB *ConfigDB
	StateDB  *StateDB

	// SkipFrrcfgdCheck bypasses the frrcfgd precondition at connect time.
	// Set to true for provisioning, which writes docker_routing_config_mode=unified
	// as part of the composite and restarts bgp afterward.
	SkipFrrcfgdCheck bool

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

// NewDevice creates a new device instance
func NewDevice(name string, profile *spec.ResolvedProfile) *Device {
	return &Device{
		Name:    name,
		Profile: profile,
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

	// Verify frrcfgd (unified config mode) is enabled. Without it, dynamic
	// CONFIG_DB entries (BGP_NEIGHBOR, BGP_GLOBALS, etc.) are silently ignored
	// by bgpcfgd, and FRR never programs the peers newtron configures.
	// Skipped for provisioning, which writes unified mode + restarts bgp.
	if !d.SkipFrrcfgdCheck {
		if err := d.requireFrrcfgd(); err != nil {
			d.client.Close()
			return err
		}
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

// StateClient returns the underlying StateDB client for direct access
func (d *Device) StateClient() *StateDBClient {
	return d.stateClient
}

// AppDBClient returns the APP_DB client for route verification.
func (d *Device) AppDBClient() *AppDBClient {
	return d.applClient
}

// AsicDBClient returns the ASIC_DB client for ASIC-level verification.
func (d *Device) AsicDBClient() *AsicDBClient {
	return d.asicClient
}

// ConnAddr returns the Redis connection address (local tunnel or direct).
func (d *Device) ConnAddr() string {
	if d.tunnel != nil {
		return d.tunnel.LocalAddr()
	}
	return fmt.Sprintf("%s:6379", d.Profile.MgmtIP)
}

// Tunnel returns the SSH tunnel for direct access (e.g., newtrun SSH commands).
// Returns nil if no SSH tunnel is configured (direct Redis connection).
func (d *Device) Tunnel() *SSHTunnel {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tunnel
}

// FrrcfgdMetadataFields returns the DEVICE_METADATA fields that enable frrcfgd
// (unified config mode). This is the single source of truth for these fields —
// used by InitDevice, the topology provisioner, and newtlab boot patches.
func FrrcfgdMetadataFields() map[string]string {
	return map[string]string{
		"docker_routing_config_mode": "unified",
		"frr_mgmt_framework_config":  "true",
	}
}

// IsUnifiedConfigMode returns true if the device has frrcfgd (unified config
// mode) enabled in DEVICE_METADATA.
func (d *Device) IsUnifiedConfigMode() bool {
	if d.ConfigDB == nil || d.ConfigDB.DeviceMetadata == nil {
		return false
	}
	localhost, ok := d.ConfigDB.DeviceMetadata["localhost"]
	if !ok {
		return false
	}
	return localhost["docker_routing_config_mode"] == "unified"
}

// requireFrrcfgd checks that the device uses frrcfgd (unified config mode).
// bgpcfgd (the default on community sonic-vs) silently ignores dynamic
// CONFIG_DB entries like BGP_NEIGHBOR, making newtron's BGP configuration
// invisible to FRR. frrcfgd processes all CONFIG_DB tables correctly.
func (d *Device) requireFrrcfgd() error {
	if d.ConfigDB == nil || d.ConfigDB.DeviceMetadata == nil {
		return fmt.Errorf("%s: cannot read DEVICE_METADATA — CONFIG_DB may be empty", d.Name)
	}
	localhost, ok := d.ConfigDB.DeviceMetadata["localhost"]
	if !ok {
		return fmt.Errorf("%s: DEVICE_METADATA|localhost not found in CONFIG_DB", d.Name)
	}
	mode := localhost["docker_routing_config_mode"]
	if mode != "unified" {
		return fmt.Errorf(
			"%s: frrcfgd not enabled (docker_routing_config_mode=%q, need \"unified\")\n"+
				"  newtron requires frrcfgd (unified config mode) to operate.\n"+
				"  Without it, BGP_NEIGHBOR and other CONFIG_DB entries are silently ignored.\n\n"+
				"  To fix, run:  newtron %s init",
			d.Name, mode, d.Name,
		)
	}
	return nil
}
