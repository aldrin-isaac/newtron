package model

// VTEP represents a VXLAN Tunnel Endpoint
type VTEP struct {
	Name            string `json:"name"`             // e.g., "vtep1"
	SourceIP        string `json:"source_ip"`        // Loopback IP
	SourceInterface string `json:"source_interface"` // e.g., "Loopback0"
	UDPPort         int    `json:"udp_port"`         // Default: 4789
}

// VXLANTunnelMap represents a VNI to VLAN/VRF mapping
type VXLANTunnelMap struct {
	Name string `json:"name"` // Map name
	VTEP string `json:"vtep"` // Parent VTEP name
	VNI  int    `json:"vni"`  // VXLAN Network Identifier
	VLAN int    `json:"vlan"` // L2VNI: mapped VLAN ID
	VRF  string `json:"vrf"`  // L3VNI: mapped VRF name
}

// EVPNConfig represents the complete EVPN configuration for a service
type EVPNConfig struct {
	VTEP   *VTEP          `json:"vtep,omitempty"`
	L2VNIs []*L2VNIConfig `json:"l2_vnis,omitempty"`
	L3VNIs []*L3VNIConfig `json:"l3_vnis,omitempty"`
}

// L2VNIConfig represents an L2 EVPN VNI configuration
type L2VNIConfig struct {
	VNI            int    `json:"vni"`
	VLAN           int    `json:"vlan"`
	ARPSuppression bool   `json:"arp_suppression"`
	VRF            string `json:"vrf,omitempty"` // For IRB
}

// L3VNIConfig represents an L3 EVPN VNI configuration
type L3VNIConfig struct {
	VNI      int      `json:"vni"`
	VRF      string   `json:"vrf"`
	RD       string   `json:"rd"`
	ImportRT []string `json:"import_rt"`
	ExportRT []string `json:"export_rt"`
}

// EVPNNVOEntry represents EVPN NVO configuration
type EVPNNVOEntry struct {
	Name       string `json:"name"`
	SourceVTEP string `json:"source_vtep"`
}

// SVIConfig represents a VLAN interface for routing (IRB)
type SVIConfig struct {
	VLAN           int    `json:"vlan"`
	VRF            string `json:"vrf"`
	IPAddress      string `json:"ip_address"`
	AnycastGateway string `json:"anycast_gateway"`
	AnycastMAC     string `json:"anycast_mac"`
}

// SAGConfig represents Static Anycast Gateway configuration
type SAGConfig struct {
	GatewayMAC string `json:"gateway_mac"`
}

// NewVTEP creates a new VTEP with defaults
func NewVTEP(name, sourceIP string) *VTEP {
	return &VTEP{
		Name:            name,
		SourceIP:        sourceIP,
		SourceInterface: "Loopback0",
		UDPPort:         4789,
	}
}

// EVPNRouteType represents EVPN route types
type EVPNRouteType int

const (
	EVPNRouteType2 EVPNRouteType = 2 // MAC/IP Advertisement
	EVPNRouteType3 EVPNRouteType = 3 // Inclusive Multicast
	EVPNRouteType5 EVPNRouteType = 5 // IP Prefix
)

// EVPNRoute represents an EVPN route
type EVPNRoute struct {
	Type     EVPNRouteType `json:"type"`
	RD       string        `json:"rd"`
	RT       []string      `json:"rt"`
	VNI      int           `json:"vni"`
	MAC      string        `json:"mac,omitempty"`
	IP       string        `json:"ip,omitempty"`
	Prefix   string        `json:"prefix,omitempty"`
	NextHop  string        `json:"next_hop"`
	OriginAS int           `json:"origin_as,omitempty"`
}

// EVPNState represents EVPN operational state
type EVPNState struct {
	VTEPState   string   `json:"vtep_state"` // up, down
	NVOState    string   `json:"nvo_state"`  // up, down
	VNICount    int      `json:"vni_count"`
	Type2Routes int      `json:"type2_routes"` // MAC/IP routes
	Type5Routes int      `json:"type5_routes"` // IP prefix routes
	RemoteVTEPs []string `json:"remote_vteps"`
}
