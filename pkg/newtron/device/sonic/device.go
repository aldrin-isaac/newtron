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

// Tunnel returns the SSH tunnel for direct access (e.g., newtest SSH commands).
// Returns nil if no SSH tunnel is configured (direct Redis connection).
func (d *Device) Tunnel() *SSHTunnel {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tunnel
}
