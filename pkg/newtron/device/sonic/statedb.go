// Package device handles SONiC device connection and configuration via config_db/Redis.
// This file implements State DB access (Redis DB 6) for operational state.
package sonic

import (
	"context"
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
// Used by newtest's verifyStateDBExecutor for generic table/key/field assertions.
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


