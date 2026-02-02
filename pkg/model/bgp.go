package model

// BGPConfig represents BGP configuration
type BGPConfig struct {
	LocalAS         int            `json:"local_as"`
	RouterID        string         `json:"router_id"`
	Neighbors       []*BGPNeighbor `json:"neighbors,omitempty"`
	AddressFamilies []string       `json:"address_families,omitempty"` // ipv4-unicast, l2vpn-evpn, etc.

	// EVPN specific
	AdvertiseAllVNI bool `json:"advertise_all_vni,omitempty"`
	AutoRT          bool `json:"auto_rt,omitempty"` // Automatic route target derivation
}

// BGPNeighbor represents a BGP peer configuration
type BGPNeighbor struct {
	Address      string `json:"address"` // Neighbor IP address
	RemoteAS     int    `json:"remote_as"`
	Description  string `json:"description,omitempty"`
	UpdateSource string `json:"update_source,omitempty"` // Source interface for BGP session
	PeerGroup    string `json:"peer_group,omitempty"`
	LocalAddress string `json:"local_address,omitempty"`

	// Timers
	HoldTime      int `json:"hold_time,omitempty"`      // Default: 180
	KeepaliveTime int `json:"keepalive_time,omitempty"` // Default: 60

	// Address families enabled for this neighbor
	AddressFamilies []string `json:"address_families,omitempty"`

	// Route policies
	InboundPolicy  string `json:"inbound_policy,omitempty"`
	OutboundPolicy string `json:"outbound_policy,omitempty"`

	// EVPN specific
	EVPNEnabled bool `json:"evpn_enabled,omitempty"`

	// State (read-only)
	AdminStatus string `json:"admin_status,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// BGPNeighborState represents the operational state of a BGP neighbor
type BGPNeighborState struct {
	Address        string `json:"address"`
	RemoteAS       int    `json:"remote_as"`
	State          string `json:"state"` // Idle, Connect, Active, OpenSent, OpenConfirm, Established
	Uptime         string `json:"uptime"`
	PrefixReceived int    `json:"prefix_received"`
	PrefixSent     int    `json:"prefix_sent"`
	MessagesRx     int    `json:"messages_rx"`
	MessagesTx     int    `json:"messages_tx"`
	LastError      string `json:"last_error,omitempty"`
}

// BGPPeerGroup represents a BGP peer group template
type BGPPeerGroup struct {
	Name            string   `json:"name"`
	RemoteAS        int      `json:"remote_as,omitempty"` // 0 means inherit from neighbor
	UpdateSource    string   `json:"update_source,omitempty"`
	AddressFamilies []string `json:"address_families,omitempty"`

	// EVPN specific
	EVPNEnabled    bool `json:"evpn_enabled,omitempty"`
	NextHopSelf    bool `json:"next_hop_self,omitempty"`
	RouteReflector bool `json:"route_reflector,omitempty"`
}

// BGPState represents the overall BGP state
type BGPState struct {
	LocalAS          int    `json:"local_as"`
	RouterID         string `json:"router_id"`
	NeighborCount    int    `json:"neighbor_count"`
	EstablishedCount int    `json:"established_count"`
	TotalPrefixes    int    `json:"total_prefixes"`
	EVPNPrefixes     int    `json:"evpn_prefixes"`
}

// BGPAFState represents address family state
type BGPAFState struct {
	AddressFamily string `json:"address_family"`
	NeighborCount int    `json:"neighbor_count"`
	PrefixCount   int    `json:"prefix_count"`
}

// NewBGPConfig creates a new BGP configuration
func NewBGPConfig(localAS int, routerID string) *BGPConfig {
	return &BGPConfig{
		LocalAS:         localAS,
		RouterID:        routerID,
		AddressFamilies: []string{"ipv4-unicast", "l2vpn-evpn"},
	}
}

// NewBGPNeighbor creates a new BGP neighbor
func NewBGPNeighbor(address string, remoteAS int) *BGPNeighbor {
	return &BGPNeighbor{
		Address:       address,
		RemoteAS:      remoteAS,
		HoldTime:      180,
		KeepaliveTime: 60,
		Enabled:       true,
	}
}

// NewEVPNNeighbor creates a BGP neighbor configured for EVPN
func NewEVPNNeighbor(address string, remoteAS int, updateSource string) *BGPNeighbor {
	return &BGPNeighbor{
		Address:         address,
		RemoteAS:        remoteAS,
		UpdateSource:    updateSource,
		AddressFamilies: []string{"l2vpn-evpn"},
		EVPNEnabled:     true,
		HoldTime:        180,
		KeepaliveTime:   60,
		Enabled:         true,
	}
}

// AddNeighbor adds a neighbor to the BGP config
func (b *BGPConfig) AddNeighbor(neighbor *BGPNeighbor) {
	// Check if neighbor already exists
	for i, n := range b.Neighbors {
		if n.Address == neighbor.Address {
			b.Neighbors[i] = neighbor
			return
		}
	}
	b.Neighbors = append(b.Neighbors, neighbor)
}

// RemoveNeighbor removes a neighbor from the BGP config
func (b *BGPConfig) RemoveNeighbor(address string) bool {
	for i, n := range b.Neighbors {
		if n.Address == address {
			b.Neighbors = append(b.Neighbors[:i], b.Neighbors[i+1:]...)
			return true
		}
	}
	return false
}

// GetNeighbor returns a neighbor by address
func (b *BGPConfig) GetNeighbor(address string) *BGPNeighbor {
	for _, n := range b.Neighbors {
		if n.Address == address {
			return n
		}
	}
	return nil
}

// HasEVPN returns true if EVPN is enabled in address families
func (b *BGPConfig) HasEVPN() bool {
	for _, af := range b.AddressFamilies {
		if af == "l2vpn-evpn" {
			return true
		}
	}
	return false
}

// IsIBGP returns true if neighbor is iBGP (same AS)
func (n *BGPNeighbor) IsIBGP(localAS int) bool {
	return n.RemoteAS == localAS
}

// IsEBGP returns true if neighbor is eBGP (different AS)
func (n *BGPNeighbor) IsEBGP(localAS int) bool {
	return n.RemoteAS != localAS
}
