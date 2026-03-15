// Package device handles SONiC device connection and configuration via config_db/Redis.
// This file implements State DB access (Redis DB 6) for operational state.
package sonic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/newtron-network/newtron/pkg/util"
)

// StateDB mirrors SONiC's state_db structure (Redis DB 6)
// State DB contains operational/runtime state, separate from config.
type StateDB struct {
	PortTable         map[string]PortStateEntry         `json:"PORT_TABLE,omitempty"`
	LAGTable          map[string]LAGStateEntry          `json:"LAG_TABLE,omitempty"`
	LAGMemberTable    map[string]LAGMemberStateEntry    `json:"LAG_MEMBER_TABLE,omitempty"`
	VLANTable         map[string]VLANStateEntry         `json:"VLAN_TABLE,omitempty"`
	VRFTable          map[string]VRFStateEntry          `json:"VRF_TABLE,omitempty"`
	VXLANTunnelTable  map[string]VXLANTunnelStateEntry  `json:"VXLAN_TUNNEL_TABLE,omitempty"`
	BGPNeighborTable  map[string]BGPNeighborStateEntry  `json:"BGP_NEIGHBOR_TABLE,omitempty"`
	InterfaceTable    map[string]InterfaceStateEntry    `json:"INTERFACE_TABLE,omitempty"`
	NeighTable        map[string]NeighStateEntry        `json:"NEIGH_TABLE,omitempty"`
	FDBTable          map[string]FDBStateEntry          `json:"FDB_TABLE,omitempty"`
	RouteTable        map[string]RouteStateEntry        `json:"ROUTE_TABLE,omitempty"`
	TransceiverInfo   map[string]TransceiverInfoEntry   `json:"TRANSCEIVER_INFO,omitempty"`
	TransceiverStatus map[string]TransceiverStatusEntry `json:"TRANSCEIVER_STATUS,omitempty"`
}

// PortStateEntry represents interface operational state from PORT_TABLE
type PortStateEntry struct {
	AdminStatus  string `json:"admin_status,omitempty"`
	OperStatus   string `json:"oper_status,omitempty"`
	Speed        string `json:"speed,omitempty"`
	MTU          string `json:"mtu,omitempty"`
	LinkTraining string `json:"link_training,omitempty"`
}

// LAGStateEntry represents LAG operational state from LAG_TABLE
type LAGStateEntry struct {
	OperStatus string `json:"oper_status,omitempty"`
	Speed      string `json:"speed,omitempty"`
	MTU        string `json:"mtu,omitempty"`
}

// LAGMemberStateEntry represents LAG member state from LAG_MEMBER_TABLE
type LAGMemberStateEntry struct {
	OperStatus     string `json:"oper_status,omitempty"`
	CollectingDist string `json:"collecting_distributing,omitempty"`
	Selected       string `json:"selected,omitempty"`
	ActorPortNum   string `json:"actor_port_num,omitempty"`
	PartnerPortNum string `json:"partner_port_num,omitempty"`
}

// VLANStateEntry represents VLAN operational state from VLAN_TABLE
type VLANStateEntry struct {
	OperStatus string `json:"oper_status,omitempty"`
	State      string `json:"state,omitempty"`
}

// VRFStateEntry represents VRF operational state from VRF_TABLE
type VRFStateEntry struct {
	State string `json:"state,omitempty"`
}

// VXLANTunnelStateEntry represents VXLAN tunnel state from VXLAN_TUNNEL_TABLE
type VXLANTunnelStateEntry struct {
	SrcIP      string `json:"src_ip,omitempty"`
	OperStatus string `json:"operstatus,omitempty"`
}

// BGPNeighborStateEntry represents BGP neighbor state from BGP_NEIGHBOR_TABLE
type BGPNeighborStateEntry struct {
	State           string `json:"state,omitempty"`
	RemoteAS        string `json:"remote_asn,omitempty"`
	LocalAS         string `json:"local_asn,omitempty"`
	PeerGroup       string `json:"peer_group,omitempty"`
	PfxRcvd         string `json:"prefixes_received,omitempty"`
	PfxSent         string `json:"prefixes_sent,omitempty"`
	MsgRcvd         string `json:"msg_rcvd,omitempty"`
	MsgSent         string `json:"msg_sent,omitempty"`
	Uptime          string `json:"uptime,omitempty"`
	HoldTime        string `json:"holdtime,omitempty"`
	KeepaliveTime   string `json:"keepalive,omitempty"`
	ConnectRetry    string `json:"connect_retry,omitempty"`
	LastResetReason string `json:"last_reset_reason,omitempty"`
}

// InterfaceStateEntry represents interface state from INTERFACE_TABLE
type InterfaceStateEntry struct {
	VRF      string `json:"vrf,omitempty"`
	ProxyArp string `json:"proxy_arp,omitempty"`
}

// NeighStateEntry represents neighbor (ARP/NDP) state from NEIGH_TABLE
type NeighStateEntry struct {
	Family string `json:"family,omitempty"`
	MAC    string `json:"neigh,omitempty"`
	State  string `json:"state,omitempty"`
}

// FDBStateEntry represents MAC forwarding entry from FDB_TABLE
type FDBStateEntry struct {
	Port       string `json:"port,omitempty"`
	Type       string `json:"type,omitempty"`
	VNI        string `json:"vni,omitempty"`
	RemoteVTEP string `json:"remote_vtep,omitempty"`
}

// RouteStateEntry represents route from ROUTE_TABLE
type RouteStateEntry struct {
	NextHop   string `json:"nexthop,omitempty"`
	Interface string `json:"ifname,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
}

// TransceiverInfoEntry represents transceiver info
type TransceiverInfoEntry struct {
	Vendor          string `json:"vendor_name,omitempty"`
	Model           string `json:"model,omitempty"`
	SerialNum       string `json:"serial_num,omitempty"`
	HardwareVersion string `json:"hardware_version,omitempty"`
	Type            string `json:"type,omitempty"`
	MediaInterface  string `json:"media_interface,omitempty"`
}

// TransceiverStatusEntry represents transceiver status
type TransceiverStatusEntry struct {
	Present     string `json:"present,omitempty"`
	Temperature string `json:"temperature,omitempty"`
	Voltage     string `json:"voltage,omitempty"`
	TxPower     string `json:"tx_power,omitempty"`
	RxPower     string `json:"rx_power,omitempty"`
}

// StateDBClient wraps Redis client for state_db access (DB 6).
type StateDBClient struct {
	client *redis.Client
	ctx    context.Context
}

// NewStateDBClient creates a new state_db client
func NewStateDBClient(addr string) *StateDBClient {
	return &StateDBClient{
		client: redis.NewClient(&redis.Options{
			Addr: addr,
			DB:   6, // STATE_DB
		}),
		ctx: context.Background(),
	}
}

// Connect tests the connection
func (c *StateDBClient) Connect() error {
	return c.client.Ping(c.ctx).Err()
}

// Close closes the connection
func (c *StateDBClient) Close() error {
	return c.client.Close()
}

// GetEntry reads a single STATE_DB entry as raw map[string]string.
// Returns (nil, nil) if the entry does not exist.
// Used by newtrun's verifyStateDBExecutor for generic table/key/field assertions.
func (c *StateDBClient) GetEntry(table, key string) (map[string]string, error) {
	redisKey := fmt.Sprintf("%s|%s", table, key)
	vals, err := c.client.HGetAll(c.ctx, redisKey).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, nil
	}
	return vals, nil
}

// GetAll reads the entire state_db
func (c *StateDBClient) GetAll() (*StateDB, error) {
	// Get all keys using cursor-based SCAN (non-blocking, unlike KEYS *)
	keys, err := scanKeys(c.ctx, c.client, "*", 100)
	if err != nil {
		return nil, err
	}

	db := newEmptyStateDB()

	for _, key := range keys {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) < 2 {
			continue
		}
		table := parts[0]
		entry := parts[1]

		// Get hash values
		vals, err := c.client.HGetAll(c.ctx, key).Result()
		if err != nil {
			continue
		}

		if parser, ok := stateTableParsers[table]; ok {
			parser(db, entry, vals)
		}
	}

	return db, nil
}

// GetBGPNeighborState returns state for a BGP neighbor
func (c *StateDBClient) GetBGPNeighborState(vrf, neighbor string) (*BGPNeighborStateEntry, error) {
	key := fmt.Sprintf("BGP_NEIGHBOR_TABLE|%s|%s", vrf, neighbor)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		// Try without VRF (default VRF)
		key = fmt.Sprintf("BGP_NEIGHBOR_TABLE|%s", neighbor)
		vals, err = c.client.HGetAll(c.ctx, key).Result()
		if err != nil || len(vals) == 0 {
			return nil, fmt.Errorf("BGP neighbor %s not found in state_db", neighbor)
		}
	}
	return &BGPNeighborStateEntry{
		State:           vals["state"],
		RemoteAS:        vals["remote_asn"],
		LocalAS:         vals["local_asn"],
		PeerGroup:       vals["peer_group"],
		PfxRcvd:         vals["prefixes_received"],
		PfxSent:         vals["prefixes_sent"],
		MsgRcvd:         vals["msg_rcvd"],
		MsgSent:         vals["msg_sent"],
		Uptime:          vals["uptime"],
		HoldTime:        vals["holdtime"],
		KeepaliveTime:   vals["keepalive"],
		ConnectRetry:    vals["connect_retry"],
		LastResetReason: vals["last_reset_reason"],
	}, nil
}

// GetNeighbor reads a neighbor (ARP/NDP) entry from STATE_DB NEIGH_TABLE.
// The key format is "NEIGH_TABLE|<interface>|<ip>" with pipe separators.
// Returns nil (not error) if the entry does not exist.
func (c *StateDBClient) GetNeighbor(iface, ip string) (*NeighEntry, error) {
	key := fmt.Sprintf("NEIGH_TABLE|%s|%s", iface, ip)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, nil
	}
	return &NeighEntry{
		IP:        ip,
		Interface: iface,
		MAC:       vals["neigh"],
		Family:    vals["family"],
	}, nil
}

// ============================================================================
// Distributed Locking — device-lld §2.3
// ============================================================================

// acquireLockScript is a Lua script for atomic lock acquisition in STATE_DB.
// Returns 1 on success, 0 if already locked by another holder.
var acquireLockScript = redis.NewScript(`
local key = KEYS[1]
if redis.call("EXISTS", key) == 1 then
	return 0
end
redis.call("HSET", key, "holder", ARGV[1], "acquired", ARGV[2], "ttl", ARGV[3])
redis.call("EXPIRE", key, tonumber(ARGV[3]))
return 1
`)

// releaseLockScript is a Lua script for atomic lock release with holder verification.
// Returns 1 on success, 0 if holder mismatch, -1 if key doesn't exist.
var releaseLockScript = redis.NewScript(`
local key = KEYS[1]
if redis.call("EXISTS", key) == 0 then
	return -1
end
local current = redis.call("HGET", key, "holder")
if current ~= ARGV[1] then
	return 0
end
redis.call("DEL", key)
return 1
`)

// AcquireLock acquires a distributed lock for the given device in STATE_DB.
// The lock is stored as NEWTRON_LOCK|<device> with holder, acquired time, and TTL.
// Returns util.ErrDeviceLocked if the device is already locked by another holder.
func (c *StateDBClient) AcquireLock(device, holder string, ttlSeconds int) error {
	key := fmt.Sprintf("NEWTRON_LOCK|%s", device)
	now := time.Now().UTC().Format(time.RFC3339)

	result, err := acquireLockScript.Run(c.ctx, c.client, []string{key},
		holder, now, fmt.Sprintf("%d", ttlSeconds)).Int()
	if err != nil {
		return fmt.Errorf("acquiring lock for %s: %w", device, err)
	}
	if result == 0 {
		return util.ErrDeviceLocked
	}
	return nil
}

// ReleaseLock releases the distributed lock for the given device in STATE_DB.
// Returns an error if the holder does not match the current lock holder.
func (c *StateDBClient) ReleaseLock(device, holder string) error {
	key := fmt.Sprintf("NEWTRON_LOCK|%s", device)

	result, err := releaseLockScript.Run(c.ctx, c.client, []string{key}, holder).Int()
	if err != nil {
		return fmt.Errorf("releasing lock for %s: %w", device, err)
	}
	switch result {
	case 0:
		return fmt.Errorf("lock holder mismatch for %s", device)
	case -1:
		return nil // Lock doesn't exist, treat as success
	}
	return nil
}

// ============================================================================
// Operation Intent Records — crash recovery for multi-entry writes
// ============================================================================

// IntentOperation represents a single pending operation within an intent record.
type IntentOperation struct {
	Name      string            `json:"name"`
	Params    map[string]string `json:"params"`
	ReverseOp string            `json:"reverse_op,omitempty"`
	Started   *time.Time        `json:"started,omitempty"`
	Completed *time.Time        `json:"completed,omitempty"`
	Reversed  *time.Time        `json:"reversed,omitempty"`
}

// Intent phases. Empty string means "applying" (the default/forward phase).
const (
	IntentPhaseApplying    = ""
	IntentPhaseRollingBack = "rolling_back"
)

// OperationIntent is the STATE_DB record for a pending multi-entry write.
// Written before Apply begins, deleted after verification succeeds.
// If the process crashes, the intent remains for recovery.
//
// During rollback, Phase transitions to "rolling_back" and each operation
// gets a Reversed timestamp as its reverse completes. If rollback crashes,
// retry skips already-reversed operations and continues where it left off.
type OperationIntent struct {
	Holder          string            `json:"holder"`
	Created         time.Time         `json:"created"`
	Phase           string            `json:"phase,omitempty"`
	RollbackHolder  string            `json:"rollback_holder,omitempty"`
	RollbackStarted *time.Time        `json:"rollback_started,omitempty"`
	Operations      []IntentOperation `json:"operations"`
}

// ============================================================================
// Operation Intent Records — now in CONFIG_DB (persistent across reboot)
//
// Intent methods use the ConfigDBClient (DB 4) instead of StateDBClient (DB 6).
// STATE_DB is cleared on reboot; CONFIG_DB survives via config save/reload.
// Same key format and lifecycle as before — just a different Redis database.
// ============================================================================

// WriteIntent writes an operation intent to CONFIG_DB.
// Key format: NEWTRON_INTENT|<device>. No Redis EXPIRE — the intent
// persists until explicitly deleted by DeleteIntent, rollback, or clear.
func (c *ConfigDBClient) WriteIntent(device string, intent *OperationIntent) error {
	key := fmt.Sprintf("NEWTRON_INTENT|%s", device)

	opsJSON, err := json.Marshal(intent.Operations)
	if err != nil {
		return fmt.Errorf("marshaling intent operations: %w", err)
	}

	// Always write all fields — HSET merges, so omitting a field would
	// leave stale values from a previous intent on the same key.
	rbStarted := ""
	if intent.RollbackStarted != nil {
		rbStarted = intent.RollbackStarted.UTC().Format(time.RFC3339)
	}
	fields := map[string]any{
		"holder":           intent.Holder,
		"created":          intent.Created.UTC().Format(time.RFC3339),
		"operations":       string(opsJSON),
		"phase":            intent.Phase,
		"rollback_holder":  intent.RollbackHolder,
		"rollback_started": rbStarted,
	}

	if err := c.client.HSet(c.ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("writing intent for %s: %w", device, err)
	}
	return nil
}

// ReadIntent reads the operation intent from CONFIG_DB.
// Returns (nil, nil) if no intent exists.
func (c *ConfigDBClient) ReadIntent(device string) (*OperationIntent, error) {
	key := fmt.Sprintf("NEWTRON_INTENT|%s", device)

	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("reading intent for %s: %w", device, err)
	}
	if len(vals) == 0 {
		return nil, nil
	}

	created, err := time.Parse(time.RFC3339, vals["created"])
	if err != nil {
		return nil, fmt.Errorf("parsing intent created time: %w", err)
	}

	var ops []IntentOperation
	if opsStr, ok := vals["operations"]; ok && opsStr != "" {
		if err := json.Unmarshal([]byte(opsStr), &ops); err != nil {
			return nil, fmt.Errorf("parsing intent operations: %w", err)
		}
	}

	intent := &OperationIntent{
		Holder:         vals["holder"],
		Created:        created,
		Phase:          vals["phase"],
		RollbackHolder: vals["rollback_holder"],
		Operations:     ops,
	}
	if rbStarted, ok := vals["rollback_started"]; ok && rbStarted != "" {
		t, err := time.Parse(time.RFC3339, rbStarted)
		if err != nil {
			return nil, fmt.Errorf("parsing rollback_started: %w", err)
		}
		intent.RollbackStarted = &t
	}
	return intent, nil
}

// UpdateIntentOps updates the mutable fields of an existing intent in CONFIG_DB:
// operations, phase, rollback_holder, and rollback_started.
// Called during both apply (started/completed timestamps) and rollback
// (phase transition, reversed timestamps).
func (c *ConfigDBClient) UpdateIntentOps(device string, intent *OperationIntent) error {
	key := fmt.Sprintf("NEWTRON_INTENT|%s", device)

	opsJSON, err := json.Marshal(intent.Operations)
	if err != nil {
		return fmt.Errorf("marshaling intent operations: %w", err)
	}

	fields := map[string]any{
		"operations": string(opsJSON),
	}
	if intent.Phase != "" {
		fields["phase"] = intent.Phase
	}
	if intent.RollbackHolder != "" {
		fields["rollback_holder"] = intent.RollbackHolder
	}
	if intent.RollbackStarted != nil {
		fields["rollback_started"] = intent.RollbackStarted.UTC().Format(time.RFC3339)
	}

	if err := c.client.HSet(c.ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("updating intent for %s: %w", device, err)
	}
	return nil
}

// DeleteIntent removes the operation intent from CONFIG_DB.
func (c *ConfigDBClient) DeleteIntent(device string) error {
	key := fmt.Sprintf("NEWTRON_INTENT|%s", device)
	if err := c.client.Del(c.ctx, key).Err(); err != nil {
		return fmt.Errorf("deleting intent for %s: %w", device, err)
	}
	return nil
}

// ReadIntentFromStateDB reads an intent from STATE_DB (legacy location).
// Used only for one-time migration in Lock() — new intents are always in CONFIG_DB.
func (c *StateDBClient) ReadIntentFromStateDB(device string) (*OperationIntent, error) {
	key := fmt.Sprintf("NEWTRON_INTENT|%s", device)

	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("reading legacy intent for %s: %w", device, err)
	}
	if len(vals) == 0 {
		return nil, nil
	}

	created, err := time.Parse(time.RFC3339, vals["created"])
	if err != nil {
		return nil, fmt.Errorf("parsing intent created time: %w", err)
	}

	var ops []IntentOperation
	if opsStr, ok := vals["operations"]; ok && opsStr != "" {
		if err := json.Unmarshal([]byte(opsStr), &ops); err != nil {
			return nil, fmt.Errorf("parsing intent operations: %w", err)
		}
	}

	intent := &OperationIntent{
		Holder:         vals["holder"],
		Created:        created,
		Phase:          vals["phase"],
		RollbackHolder: vals["rollback_holder"],
		Operations:     ops,
	}
	if rbStarted, ok := vals["rollback_started"]; ok && rbStarted != "" {
		t, err := time.Parse(time.RFC3339, rbStarted)
		if err != nil {
			return nil, fmt.Errorf("parsing rollback_started: %w", err)
		}
		intent.RollbackStarted = &t
	}
	return intent, nil
}

// DeleteIntentFromStateDB deletes an intent from STATE_DB (legacy location).
// Used only for one-time migration.
func (c *StateDBClient) DeleteIntentFromStateDB(device string) error {
	key := fmt.Sprintf("NEWTRON_INTENT|%s", device)
	if err := c.client.Del(c.ctx, key).Err(); err != nil {
		return fmt.Errorf("deleting legacy intent for %s: %w", device, err)
	}
	return nil
}

// ============================================================================
// Device Settings — CONFIG_DB (NEWTRON_SETTINGS|global)
// ============================================================================

// DefaultMaxHistory is the default rolling history buffer size when no
// device-level override exists.
const DefaultMaxHistory = 10

// DeviceSettings holds per-device newtron operational tuning.
// Stored in CONFIG_DB as NEWTRON_SETTINGS|global.
type DeviceSettings struct {
	MaxHistory int `json:"max_history"`
}

// ReadSettings reads NEWTRON_SETTINGS|global from CONFIG_DB.
// Returns defaults if the key does not exist.
func (c *ConfigDBClient) ReadSettings() (*DeviceSettings, error) {
	vals, err := c.client.HGetAll(c.ctx, "NEWTRON_SETTINGS|global").Result()
	if err != nil {
		return nil, fmt.Errorf("reading NEWTRON_SETTINGS: %w", err)
	}

	settings := &DeviceSettings{MaxHistory: DefaultMaxHistory}
	if v, ok := vals["max_history"]; ok {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
			settings.MaxHistory = n
		}
	}
	return settings, nil
}

// WriteSettings writes NEWTRON_SETTINGS|global to CONFIG_DB.
func (c *ConfigDBClient) WriteSettings(s *DeviceSettings) error {
	fields := map[string]any{
		"max_history": fmt.Sprintf("%d", s.MaxHistory),
	}
	return c.client.HSet(c.ctx, "NEWTRON_SETTINGS|global", fields).Err()
}

// ============================================================================
// Rolling History — CONFIG_DB (bounded per device settings)
// ============================================================================

// HistoryEntry represents a completed commit archived for rollback.
type HistoryEntry struct {
	Sequence   int               `json:"sequence"`
	Holder     string            `json:"holder"`
	Timestamp  time.Time         `json:"timestamp"`
	Operations []IntentOperation `json:"operations"`
}

// WriteHistory writes a history entry to CONFIG_DB.
// Key format: NEWTRON_HISTORY|<device>|<sequence>
func (c *ConfigDBClient) WriteHistory(device string, entry *HistoryEntry) error {
	key := fmt.Sprintf("NEWTRON_HISTORY|%s|%d", device, entry.Sequence)

	opsJSON, err := json.Marshal(entry.Operations)
	if err != nil {
		return fmt.Errorf("marshaling history operations: %w", err)
	}

	fields := map[string]any{
		"holder":     entry.Holder,
		"timestamp":  entry.Timestamp.UTC().Format(time.RFC3339),
		"operations": string(opsJSON),
	}

	if err := c.client.HSet(c.ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("writing history entry for %s seq %d: %w", device, entry.Sequence, err)
	}
	return nil
}

// ReadHistory reads all history entries for a device from CONFIG_DB.
// Returns entries sorted by sequence descending (newest first).
func (c *ConfigDBClient) ReadHistory(device string) ([]*HistoryEntry, error) {
	pattern := fmt.Sprintf("NEWTRON_HISTORY|%s|*", device)
	keys, err := scanKeys(c.ctx, c.client, pattern, 100)
	if err != nil {
		return nil, fmt.Errorf("scanning history for %s: %w", device, err)
	}

	var entries []*HistoryEntry
	for _, key := range keys {
		vals, err := c.client.HGetAll(c.ctx, key).Result()
		if err != nil || len(vals) == 0 {
			continue
		}

		// Parse sequence from key: NEWTRON_HISTORY|device|seq
		parts := strings.SplitN(key, "|", 3)
		if len(parts) < 3 {
			continue
		}
		var seq int
		if _, err := fmt.Sscanf(parts[2], "%d", &seq); err != nil {
			continue
		}

		ts, err := time.Parse(time.RFC3339, vals["timestamp"])
		if err != nil {
			continue
		}

		var ops []IntentOperation
		if opsStr, ok := vals["operations"]; ok && opsStr != "" {
			if err := json.Unmarshal([]byte(opsStr), &ops); err != nil {
				continue
			}
		}

		entries = append(entries, &HistoryEntry{
			Sequence:   seq,
			Holder:     vals["holder"],
			Timestamp:  ts,
			Operations: ops,
		})
	}

	// Sort by sequence descending (newest first)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].Sequence < entries[j].Sequence {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	return entries, nil
}

// UpdateHistory updates a history entry's operations (for marking ops as reversed).
func (c *ConfigDBClient) UpdateHistory(device string, entry *HistoryEntry) error {
	return c.WriteHistory(device, entry)
}

// DeleteHistory deletes a single history entry.
func (c *ConfigDBClient) DeleteHistory(device string, seq int) error {
	key := fmt.Sprintf("NEWTRON_HISTORY|%s|%d", device, seq)
	if err := c.client.Del(c.ctx, key).Err(); err != nil {
		return fmt.Errorf("deleting history entry %s seq %d: %w", device, seq, err)
	}
	return nil
}


