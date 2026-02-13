// Package device handles SONiC device connection and configuration via config_db/Redis.
// This file implements State DB access (Redis DB 6) for operational state.
package device

import (
	"context"
	"fmt"
	"strconv"
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

	db := &StateDB{
		PortTable:         make(map[string]PortStateEntry),
		LAGTable:          make(map[string]LAGStateEntry),
		LAGMemberTable:    make(map[string]LAGMemberStateEntry),
		VLANTable:         make(map[string]VLANStateEntry),
		VRFTable:          make(map[string]VRFStateEntry),
		VXLANTunnelTable:  make(map[string]VXLANTunnelStateEntry),
		BGPNeighborTable:  make(map[string]BGPNeighborStateEntry),
		InterfaceTable:    make(map[string]InterfaceStateEntry),
		NeighTable:        make(map[string]NeighStateEntry),
		FDBTable:          make(map[string]FDBStateEntry),
		RouteTable:        make(map[string]RouteStateEntry),
		TransceiverInfo:   make(map[string]TransceiverInfoEntry),
		TransceiverStatus: make(map[string]TransceiverStatusEntry),
	}

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

		c.parseEntry(db, table, entry, vals)
	}

	return db, nil
}

func (c *StateDBClient) parseEntry(db *StateDB, table, entry string, vals map[string]string) {
	switch table {
	case "PORT_TABLE":
		db.PortTable[entry] = PortStateEntry{
			AdminStatus:  vals["admin_status"],
			OperStatus:   vals["oper_status"],
			Speed:        vals["speed"],
			MTU:          vals["mtu"],
			LinkTraining: vals["link_training"],
		}
	case "LAG_TABLE":
		db.LAGTable[entry] = LAGStateEntry{
			OperStatus: vals["oper_status"],
			Speed:      vals["speed"],
			MTU:        vals["mtu"],
		}
	case "LAG_MEMBER_TABLE":
		db.LAGMemberTable[entry] = LAGMemberStateEntry{
			OperStatus:     vals["oper_status"],
			CollectingDist: vals["collecting_distributing"],
			Selected:       vals["selected"],
			ActorPortNum:   vals["actor_port_num"],
			PartnerPortNum: vals["partner_port_num"],
		}
	case "VLAN_TABLE":
		db.VLANTable[entry] = VLANStateEntry{
			OperStatus: vals["oper_status"],
			State:      vals["state"],
		}
	case "VRF_TABLE":
		db.VRFTable[entry] = VRFStateEntry{
			State: vals["state"],
		}
	case "VXLAN_TUNNEL_TABLE":
		db.VXLANTunnelTable[entry] = VXLANTunnelStateEntry{
			SrcIP:      vals["src_ip"],
			OperStatus: vals["operstatus"],
		}
	case "BGP_NEIGHBOR_TABLE", "NEIGH_STATE_TABLE":
		// BGP neighbor state - key is VRF|neighbor_ip
		db.BGPNeighborTable[entry] = BGPNeighborStateEntry{
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
		}
	case "INTERFACE_TABLE":
		db.InterfaceTable[entry] = InterfaceStateEntry{
			VRF:      vals["vrf"],
			ProxyArp: vals["proxy_arp"],
		}
	case "NEIGH_TABLE":
		db.NeighTable[entry] = NeighStateEntry{
			Family: vals["family"],
			MAC:    vals["neigh"],
			State:  vals["state"],
		}
	case "FDB_TABLE":
		db.FDBTable[entry] = FDBStateEntry{
			Port:       vals["port"],
			Type:       vals["type"],
			VNI:        vals["vni"],
			RemoteVTEP: vals["remote_vtep"],
		}
	case "ROUTE_TABLE":
		db.RouteTable[entry] = RouteStateEntry{
			NextHop:   vals["nexthop"],
			Interface: vals["ifname"],
			Protocol:  vals["protocol"],
		}
	case "TRANSCEIVER_INFO":
		db.TransceiverInfo[entry] = TransceiverInfoEntry{
			Vendor:          vals["vendor_name"],
			Model:           vals["model"],
			SerialNum:       vals["serial_num"],
			HardwareVersion: vals["hardware_version"],
			Type:            vals["type"],
			MediaInterface:  vals["media_interface"],
		}
	case "TRANSCEIVER_STATUS":
		db.TransceiverStatus[entry] = TransceiverStatusEntry{
			Present:     vals["present"],
			Temperature: vals["temperature"],
			Voltage:     vals["voltage"],
			TxPower:     vals["tx_power"],
			RxPower:     vals["rx_power"],
		}
	}
}

// GetPortState returns operational state for a specific interface from PORT_TABLE.
func (c *StateDBClient) GetPortState(name string) (*PortStateEntry, error) {
	key := fmt.Sprintf("PORT_TABLE|%s", name)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("interface %s not found in state_db PORT_TABLE", name)
	}
	return &PortStateEntry{
		AdminStatus:  vals["admin_status"],
		OperStatus:   vals["oper_status"],
		Speed:        vals["speed"],
		MTU:          vals["mtu"],
		LinkTraining: vals["link_training"],
	}, nil
}

// GetLAGState returns operational state for a specific LAG
func (c *StateDBClient) GetLAGState(name string) (*LAGStateEntry, error) {
	key := fmt.Sprintf("LAG_TABLE|%s", name)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("LAG %s not found in state_db", name)
	}
	return &LAGStateEntry{
		OperStatus: vals["oper_status"],
		Speed:      vals["speed"],
		MTU:        vals["mtu"],
	}, nil
}

// GetLAGMemberState returns state for a LAG member
func (c *StateDBClient) GetLAGMemberState(lag, member string) (*LAGMemberStateEntry, error) {
	key := fmt.Sprintf("LAG_MEMBER_TABLE|%s|%s", lag, member)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("LAG member %s|%s not found in state_db", lag, member)
	}
	return &LAGMemberStateEntry{
		OperStatus:     vals["oper_status"],
		CollectingDist: vals["collecting_distributing"],
		Selected:       vals["selected"],
		ActorPortNum:   vals["actor_port_num"],
		PartnerPortNum: vals["partner_port_num"],
	}, nil
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

// GetVXLANTunnelState returns state for VXLAN tunnel
func (c *StateDBClient) GetVXLANTunnelState(name string) (*VXLANTunnelStateEntry, error) {
	key := fmt.Sprintf("VXLAN_TUNNEL_TABLE|%s", name)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("VXLAN tunnel %s not found in state_db", name)
	}
	return &VXLANTunnelStateEntry{
		SrcIP:      vals["src_ip"],
		OperStatus: vals["operstatus"],
	}, nil
}

// GetRemoteVTEPs returns list of discovered remote VTEPs from EVPN
func (c *StateDBClient) GetRemoteVTEPs() ([]string, error) {
	// Remote VTEPs are discovered via EVPN and stored in VXLAN_TUNNEL_TABLE
	// with entries like VXLAN_TUNNEL_TABLE|remote_vtep_ip
	pattern := "VXLAN_TUNNEL_TABLE|*"
	keys, err := scanKeys(c.ctx, c.client, pattern, 100)
	if err != nil {
		return nil, err
	}

	var vteps []string
	for _, key := range keys {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 {
			vteps = append(vteps, parts[1])
		}
	}
	return vteps, nil
}

// GetRouteCount returns the number of routes in a VRF
func (c *StateDBClient) GetRouteCount(vrf string) (int, error) {
	var pattern string
	if vrf == "" || vrf == "default" {
		pattern = "ROUTE_TABLE|*"
	} else {
		pattern = fmt.Sprintf("ROUTE_TABLE|%s|*", vrf)
	}

	keys, err := scanKeys(c.ctx, c.client, pattern, 100)
	if err != nil {
		return 0, err
	}
	return len(keys), nil
}

// GetFDBCount returns the number of MAC entries for a VLAN
func (c *StateDBClient) GetFDBCount(vlan int) (int, error) {
	pattern := fmt.Sprintf("FDB_TABLE|Vlan%d|*", vlan)
	keys, err := scanKeys(c.ctx, c.client, pattern, 100)
	if err != nil {
		return 0, err
	}
	return len(keys), nil
}

// GetTransceiverInfo returns transceiver info for a port
func (c *StateDBClient) GetTransceiverInfo(port string) (*TransceiverInfoEntry, error) {
	key := fmt.Sprintf("TRANSCEIVER_INFO|%s", port)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("transceiver info for %s not found", port)
	}
	return &TransceiverInfoEntry{
		Vendor:          vals["vendor_name"],
		Model:           vals["model"],
		SerialNum:       vals["serial_num"],
		HardwareVersion: vals["hardware_version"],
		Type:            vals["type"],
		MediaInterface:  vals["media_interface"],
	}, nil
}

// GetTransceiverStatus returns transceiver status for a port
func (c *StateDBClient) GetTransceiverStatus(port string) (*TransceiverStatusEntry, error) {
	key := fmt.Sprintf("TRANSCEIVER_STATUS|%s", port)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("transceiver status for %s not found", port)
	}
	return &TransceiverStatusEntry{
		Present:     vals["present"],
		Temperature: vals["temperature"],
		Voltage:     vals["voltage"],
		TxPower:     vals["tx_power"],
		RxPower:     vals["rx_power"],
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

// GetLockHolder returns the current lock holder and acquisition time for the device.
// Returns ("", zero, nil) if no lock is held.
func (c *StateDBClient) GetLockHolder(device string) (string, time.Time, error) {
	key := fmt.Sprintf("NEWTRON_LOCK|%s", device)

	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("getting lock holder for %s: %w", device, err)
	}
	if len(vals) == 0 {
		return "", time.Time{}, nil
	}

	holder := vals["holder"]
	acquired := time.Time{}
	if ts, ok := vals["acquired"]; ok {
		acquired, _ = time.Parse(time.RFC3339, ts)
	}
	return holder, acquired, nil
}

// PopulateDeviceState fills DeviceState from StateDB data
func PopulateDeviceState(state *DeviceState, stateDB *StateDB, configDB *ConfigDB) {
	// Populate interface state
	for name, portState := range stateDB.PortTable {
		intfState := &InterfaceState{
			Name:        name,
			AdminStatus: portState.AdminStatus,
			OperStatus:  portState.OperStatus,
			Speed:       portState.Speed,
		}

		// Parse MTU
		if portState.MTU != "" {
			intfState.MTU, _ = strconv.Atoi(portState.MTU)
		}

		// Get VRF binding from config_db
		if configDB != nil {
			if intfEntry, ok := configDB.Interface[name]; ok {
				intfState.VRF = intfEntry.VRFName
			}
		}

		state.Interfaces[name] = intfState
	}

	// Populate PortChannel state
	for name, lagState := range stateDB.LAGTable {
		pcState := &PortChannelState{
			Name:       name,
			OperStatus: lagState.OperStatus,
		}

		// Get members from LAG_MEMBER_TABLE
		for key, memberState := range stateDB.LAGMemberTable {
			parts := strings.SplitN(key, "|", 2)
			if len(parts) == 2 && parts[0] == name {
				memberName := parts[1]
				pcState.Members = append(pcState.Members, memberName)
				if memberState.OperStatus == "up" && memberState.Selected == "true" {
					pcState.ActiveMembers = append(pcState.ActiveMembers, memberName)
				}
			}
		}

		// Get admin status from config_db
		if configDB != nil {
			if pcEntry, ok := configDB.PortChannel[name]; ok {
				pcState.AdminStatus = pcEntry.AdminStatus
			}
		}

		state.PortChannels[name] = pcState
	}

	// Populate VLAN state
	for name, vlanState := range stateDB.VLANTable {
		var id int
		fmt.Sscanf(name, "Vlan%d", &id)
		if id > 0 {
			state.VLANs[id] = &VLANState{
				ID:         id,
				OperStatus: vlanState.OperStatus,
			}
		}
	}

	// Populate VRF state
	for name, vrfState := range stateDB.VRFTable {
		state.VRFs[name] = &VRFState{
			Name:  name,
			State: vrfState.State,
		}

		// Get L3VNI from config_db
		if configDB != nil {
			if vrfEntry, ok := configDB.VRF[name]; ok && vrfEntry.VNI != "" {
				state.VRFs[name].L3VNI, _ = strconv.Atoi(vrfEntry.VNI)
			}
		}
	}

	// Populate BGP state
	state.BGP = &BGPState{
		Neighbors: make(map[string]*BGPNeighborState),
	}

	// Get local AS and router ID from config_db
	if configDB != nil {
		if globals, ok := configDB.BGPGlobals["default"]; ok {
			state.BGP.LocalAS, _ = strconv.Atoi(globals.LocalASN)
			state.BGP.RouterID = globals.RouterID
		}
	}

	// Populate BGP neighbor states
	for key, neighborState := range stateDB.BGPNeighborTable {
		// Key format could be "vrf|ip" or just "ip"
		parts := strings.SplitN(key, "|", 2)
		var neighborIP string
		if len(parts) == 2 {
			neighborIP = parts[1]
		} else {
			neighborIP = parts[0]
		}

		remoteAS, _ := strconv.Atoi(neighborState.RemoteAS)
		pfxRcvd, _ := strconv.Atoi(neighborState.PfxRcvd)
		pfxSent, _ := strconv.Atoi(neighborState.PfxSent)

		state.BGP.Neighbors[neighborIP] = &BGPNeighborState{
			Address:  neighborIP,
			RemoteAS: remoteAS,
			State:    neighborState.State,
			PfxRcvd:  pfxRcvd,
			PfxSent:  pfxSent,
			Uptime:   neighborState.Uptime,
		}
	}

	// Populate EVPN state
	state.EVPN = &EVPNState{}

	// Get VTEP state
	for name, tunnelState := range stateDB.VXLANTunnelTable {
		// Check if this is a remote VTEP (learned via EVPN) or local VTEP
		if configDB != nil {
			if _, ok := configDB.VXLANTunnel[name]; ok {
				// Local VTEP
				state.EVPN.VTEPState = tunnelState.OperStatus
			} else {
				// Remote VTEP
				state.EVPN.RemoteVTEPs = append(state.EVPN.RemoteVTEPs, name)
			}
		}
	}

	// Count VNIs from config_db
	if configDB != nil {
		state.EVPN.VNICount = len(configDB.VXLANTunnelMap)
	}
}
