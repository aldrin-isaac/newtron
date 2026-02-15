// Package device provides NOS-independent types for device state, configuration
// changes, and route verification. The SONiC-specific implementation lives in
// the device/sonic sub-package.
package device

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

// InterfaceSummary is a compact interface summary
type InterfaceSummary struct {
	Name        string
	AdminStatus string
	Speed       string
	IPAddress   string
	VRF         string
	Service     string
	LAGMember   string
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
