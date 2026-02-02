package model

// VRF represents a Virtual Routing and Forwarding instance
type VRF struct {
	Name        string `json:"name"` // VRF name
	Description string `json:"description"`

	// L3VNI for EVPN
	L3VNI int `json:"l3_vni,omitempty"`

	// Route Distinguisher
	RD string `json:"rd,omitempty"`

	// Route Targets
	ImportRT []string `json:"import_rt,omitempty"`
	ExportRT []string `json:"export_rt,omitempty"`

	// Interfaces bound to this VRF
	Interfaces []string `json:"interfaces,omitempty"`

	// VLANs with SVIs in this VRF
	VLANs []int `json:"vlans,omitempty"`

	// Routing protocol configuration
	BGPEnabled bool `json:"bgp_enabled,omitempty"`

	// VRF Type: "interface" (per-interface) or "shared"
	VRFType string `json:"vrf_type,omitempty"`
}

// VRFState represents the operational state of a VRF
type VRFState struct {
	Name       string   `json:"name"`
	State      string   `json:"state"` // up, down
	Interfaces []string `json:"interfaces"`
	RouteCount int      `json:"route_count"`
	EVPNState  string   `json:"evpn_state,omitempty"` // up, down
}

// NewVRF creates a new VRF with defaults
func NewVRF(name string) *VRF {
	return &VRF{
		Name:    name,
		VRFType: "interface",
	}
}

// NewSharedVRF creates a new shared VRF
func NewSharedVRF(name string, l3vni int, importRT, exportRT []string) *VRF {
	return &VRF{
		Name:     name,
		L3VNI:    l3vni,
		ImportRT: importRT,
		ExportRT: exportRT,
		VRFType:  "shared",
	}
}

// HasEVPN returns true if this VRF has EVPN (L3VNI) configured
func (v *VRF) HasEVPN() bool {
	return v.L3VNI > 0
}

// AddInterface adds an interface to the VRF
func (v *VRF) AddInterface(iface string) {
	for _, i := range v.Interfaces {
		if i == iface {
			return
		}
	}
	v.Interfaces = append(v.Interfaces, iface)
}

// RemoveInterface removes an interface from the VRF
func (v *VRF) RemoveInterface(iface string) bool {
	for i, intf := range v.Interfaces {
		if intf == iface {
			v.Interfaces = append(v.Interfaces[:i], v.Interfaces[i+1:]...)
			return true
		}
	}
	return false
}

// HasInterface returns true if the interface is in this VRF
func (v *VRF) HasInterface(iface string) bool {
	for _, i := range v.Interfaces {
		if i == iface {
			return true
		}
	}
	return false
}

// AddVLAN adds a VLAN to the VRF
func (v *VRF) AddVLAN(vlanID int) {
	for _, id := range v.VLANs {
		if id == vlanID {
			return
		}
	}
	v.VLANs = append(v.VLANs, vlanID)
}

// IsEmpty returns true if the VRF has no interfaces or VLANs
func (v *VRF) IsEmpty() bool {
	return len(v.Interfaces) == 0 && len(v.VLANs) == 0
}

// IsShared returns true if this is a shared VRF
func (v *VRF) IsShared() bool {
	return v.VRFType == "shared"
}
