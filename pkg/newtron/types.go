// Package newtron provides the top-level API for the newtron network automation system.
//
// This file defines all types, constants, request/response structs, and error types
// used by the newtron API layer. It imports only stdlib packages.
package newtron

import (
	"fmt"
	"time"
)

// ============================================================================
// Service Type Constants
// ============================================================================

const (
	ServiceTypeEVPNIRB     = "evpn-irb"     // L2+L3 overlay: requires ipvpn + macvpn
	ServiceTypeEVPNBridged = "evpn-bridged"  // L2 overlay: requires macvpn
	ServiceTypeEVPNRouted  = "evpn-routed"   // L3 overlay: requires ipvpn
	ServiceTypeIRB         = "irb"           // Local L2+L3: vlan + ip at apply time
	ServiceTypeBridged     = "bridged"       // Local L2: vlan at apply time
	ServiceTypeRouted      = "routed"        // Local L3: ip at apply time
)

// ============================================================================
// Execution Options
// ============================================================================

// ExecOpts controls dry-run vs execute behavior.
type ExecOpts struct {
	Execute bool // true = apply; false = dry-run preview
	NoSave  bool // skip config save after apply
}

// ============================================================================
// Write Result Types
// ============================================================================

// WriteResult wraps the outcome of a configuration write operation.
type WriteResult struct {
	Preview       string              // Human-readable diff preview (populated in dry-run)
	ChangeCount   int                 // Number of CONFIG_DB changes in this operation
	Applied       bool                // True if changes were applied to the device
	Verified      bool                // True if post-apply verification passed
	Saved         bool                // True if config was saved after apply
	Verification  *VerificationResult // Populated after apply; nil in dry-run
}

// VerificationResult reports ChangeSet verification outcome.
type VerificationResult struct {
	Passed int                 // entries that matched
	Failed int                 // entries missing or mismatched
	Errors []VerificationError // details of each failure
}

// VerificationError describes a single verification failure.
type VerificationError struct {
	Table    string `json:"table"`
	Key      string `json:"key"`
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"` // "" if missing
}

// ============================================================================
// Error Types
// ============================================================================

// NotFoundError indicates a requested resource does not exist.
type NotFoundError struct {
	Resource string
	Name     string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s '%s' not found", e.Resource, e.Name)
}

// ValidationError indicates invalid input.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("validation error: %s", e.Message)
}

// VerificationFailedError indicates post-apply verification failed.
type VerificationFailedError struct {
	Device  string
	Passed  int
	Failed  int
	Total   int
	Message string
}

func (e *VerificationFailedError) Error() string {
	return fmt.Sprintf("verification failed on %s: %d/%d entries did not persist", e.Device, e.Failed, e.Total)
}

// ============================================================================
// Config Types for Write Operations
// ============================================================================

// VLANConfig holds parameters for creating a VLAN.
type VLANConfig struct {
	VlanID      int
	Description string
}

// SVIConfig holds parameters for configuring an SVI (VLAN interface).
type SVIConfig struct {
	VlanID     int
	VRF        string
	IPAddress  string
	AnycastMAC string
}

// VRFConfig holds parameters for creating a VRF.
type VRFConfig struct {
	Name string
}

// BGPNeighborConfig holds parameters for adding a BGP neighbor.
type BGPNeighborConfig struct {
	VRF         string
	Interface   string
	RemoteAS    int
	NeighborIP  string
	Description string
}

// ACLTableConfig holds parameters for creating an ACL table.
type ACLTableConfig struct {
	Name        string
	Type        string
	Stage       string
	Ports       string
	Description string
}

// ACLRuleConfig holds parameters for adding an ACL rule.
type ACLRuleConfig struct {
	ACLName  string
	RuleName string
	Priority int
	Action   string
	SrcIP    string
	DstIP    string
	Protocol string
	SrcPort  string
	DstPort  string
}

// PortChannelConfig holds parameters for creating a LAG/PortChannel.
type PortChannelConfig struct {
	Name     string
	Members  []string
	MinLinks int
	FastRate bool
	Fallback bool
	MTU      int
}

// ApplyServiceOpts contains options for applying a service to an interface.
type ApplyServiceOpts struct {
	IPAddress string            // IP address for routed/IRB services (e.g., "10.1.1.1/30")
	VLAN      int               // VLAN ID for local types (irb, bridged) — overlay types use macvpnDef.VlanID
	PeerAS    int               // BGP peer AS number (for services with routing.peer_as="request")
	Params    map[string]string // topology params (peer_as, route_reflector_client, next_hop_self)
}

// ============================================================================
// Service Operation Request Types
// ============================================================================

// ApplyServiceRequest is the request for applying a service to an interface.
type ApplyServiceRequest struct {
	Device    string
	Interface string
	Service   string
	IPAddress string
	PeerAS    int
}

// RemoveServiceRequest is the request for removing a service from an interface.
type RemoveServiceRequest struct {
	Device    string
	Interface string
}

// RefreshServiceRequest is the request for refreshing a service on an interface.
type RefreshServiceRequest struct {
	Device    string
	Interface string
}

// ============================================================================
// VLAN Operation Request Types
// ============================================================================

// CreateVLANRequest is the request for creating a VLAN.
type CreateVLANRequest struct {
	Device      string
	VlanID      int
	Description string
}

// DeleteVLANRequest is the request for deleting a VLAN.
type DeleteVLANRequest struct {
	Device string
	VlanID int
}

// AddVLANMemberRequest is the request for adding a member to a VLAN.
type AddVLANMemberRequest struct {
	Device    string
	VlanID    int
	Interface string
	Tagged    bool
}

// RemoveVLANMemberRequest is the request for removing a member from a VLAN.
type RemoveVLANMemberRequest struct {
	Device    string
	VlanID    int
	Interface string
}

// ConfigureSVIRequest is the request for configuring an SVI.
type ConfigureSVIRequest struct {
	Device     string
	VlanID     int
	VRF        string
	IPAddress  string
	AnycastMAC string
}

// RemoveSVIRequest is the request for removing an SVI.
type RemoveSVIRequest struct {
	Device string
	VlanID int
}

// BindMACVPNRequest is the request for binding a MACVPN to a VLAN.
type BindMACVPNRequest struct {
	Device    string
	VlanID    int
	MACVPName string
}

// UnbindMACVPNRequest is the request for unbinding a MACVPN from a VLAN.
type UnbindMACVPNRequest struct {
	Device string
	VlanID int
}

// ============================================================================
// VRF Operation Request Types
// ============================================================================

// CreateVRFRequest is the request for creating a VRF.
type CreateVRFRequest struct {
	Device string
	Name   string
}

// DeleteVRFRequest is the request for deleting a VRF.
type DeleteVRFRequest struct {
	Device string
	Name   string
}

// AddVRFInterfaceRequest is the request for adding an interface to a VRF.
type AddVRFInterfaceRequest struct {
	Device    string
	VRF       string
	Interface string
}

// RemoveVRFInterfaceRequest is the request for removing an interface from a VRF.
type RemoveVRFInterfaceRequest struct {
	Device    string
	VRF       string
	Interface string
}

// BindIPVPNRequest is the request for binding an IPVPN to a VRF.
type BindIPVPNRequest struct {
	Device string
	VRF    string
	IPVPN  string
}

// UnbindIPVPNRequest is the request for unbinding an IPVPN from a VRF.
type UnbindIPVPNRequest struct {
	Device string
	VRF    string
}

// AddBGPNeighborRequest is the request for adding a BGP neighbor.
type AddBGPNeighborRequest struct {
	Device      string
	VRF         string
	Interface   string
	RemoteAS    int
	NeighborIP  string
	Description string
}

// RemoveBGPNeighborRequest is the request for removing a BGP neighbor.
type RemoveBGPNeighborRequest struct {
	Device     string
	VRF        string
	Interface  string // explicit interface (newtrun)
	NeighborIP string // explicit neighbor IP (newtrun)
	Target     string // ambiguous interface-or-IP (CLI compat)
}

// AddStaticRouteRequest is the request for adding a static route.
type AddStaticRouteRequest struct {
	Device  string
	VRF     string
	Prefix  string
	NextHop string
	Metric  int
}

// RemoveStaticRouteRequest is the request for removing a static route.
type RemoveStaticRouteRequest struct {
	Device string
	VRF    string
	Prefix string
}

// ============================================================================
// EVPN Operation Request Types
// ============================================================================

// SetupEVPNRequest is the request for setting up EVPN.
type SetupEVPNRequest struct {
	Device   string
	SourceIP string
}

// TeardownEVPNRequest is the request for tearing down EVPN.
type TeardownEVPNRequest struct {
	Device string
}

// ============================================================================
// ACL Operation Request Types
// ============================================================================

// CreateACLRequest is the request for creating an ACL table.
type CreateACLRequest struct {
	Device      string
	Name        string
	Type        string
	Stage       string
	Ports       string
	Description string
}

// DeleteACLRequest is the request for deleting an ACL table.
type DeleteACLRequest struct {
	Device string
	Name   string
}

// AddACLRuleRequest is the request for adding an ACL rule.
type AddACLRuleRequest struct {
	Device   string
	ACLName  string
	RuleName string
	Priority int
	Action   string
	SrcIP    string
	DstIP    string
	Protocol string
	SrcPort  string
	DstPort  string
}

// RemoveACLRuleRequest is the request for removing an ACL rule.
type RemoveACLRuleRequest struct {
	Device   string
	ACLName  string
	RuleName string
}

// BindACLRequest is the request for binding an ACL to an interface.
type BindACLRequest struct {
	Device    string
	ACLName   string
	Interface string
	Direction string
}

// UnbindACLRequest is the request for unbinding an ACL from an interface.
type UnbindACLRequest struct {
	Device    string
	ACLName   string
	Interface string
}

// ============================================================================
// QoS Operation Request Types
// ============================================================================

// ApplyQoSRequest is the request for applying a QoS policy to an interface.
type ApplyQoSRequest struct {
	Device    string
	Interface string
	Policy    string
}

// RemoveQoSRequest is the request for removing a QoS policy from an interface.
type RemoveQoSRequest struct {
	Device    string
	Interface string
}

// ============================================================================
// Interface Operation Request Types
// ============================================================================

// SetInterfacePropertyRequest is the request for setting an interface property.
type SetInterfacePropertyRequest struct {
	Device    string
	Interface string
	Property  string
	Value     string
}

// RemoveIPRequest is the request for removing an IP address from an interface.
type RemoveIPRequest struct {
	Device    string
	Interface string
	IP        string
}

// ============================================================================
// LAG Operation Request Types
// ============================================================================

// CreateLAGRequest is the request for creating a LAG/PortChannel.
type CreateLAGRequest struct {
	Device   string
	Name     string
	Members  []string
	MinLinks int
	FastRate bool
	Fallback bool
	MTU      int
}

// DeleteLAGRequest is the request for deleting a LAG/PortChannel.
type DeleteLAGRequest struct {
	Device string
	Name   string
}

// AddLAGMemberRequest is the request for adding a member to a LAG.
type AddLAGMemberRequest struct {
	Device string
	LAG    string
	Member string
}

// RemoveLAGMemberRequest is the request for removing a member from a LAG.
type RemoveLAGMemberRequest struct {
	Device string
	LAG    string
	Member string
}

// ============================================================================
// Device Operation Request Types
// ============================================================================

// CleanupRequest is the request for cleaning up orphaned device resources.
type CleanupRequest struct {
	Device string
	Type   string // "acls", "vrfs", "vnis", or empty for all
}

// CleanupSummary provides details about orphaned resources found and removed.
type CleanupSummary struct {
	OrphanedACLs        []string
	OrphanedVRFs        []string
	OrphanedVNIMappings []string
}

// ============================================================================
// BGP Operation Request Types
// ============================================================================

// ConfigureBGPRequest is the request for configuring BGP globals on a device.
type ConfigureBGPRequest struct {
	Device string
}

// RemoveBGPGlobalsRequest is the request for removing BGP globals from a device.
type RemoveBGPGlobalsRequest struct {
	Device string
}

// ============================================================================
// Loopback Operation Request Types
// ============================================================================

// ConfigureLoopbackRequest is the request for configuring a loopback interface.
type ConfigureLoopbackRequest struct {
	Device string
	IP     string
}

// RemoveLoopbackRequest is the request for removing loopback configuration.
type RemoveLoopbackRequest struct {
	Device string
}

// ============================================================================
// Provision Operation Request/Result Types
// ============================================================================

// ProvisionRequest is the request for provisioning devices.
type ProvisionRequest struct {
	Devices []string // empty = all devices in topology
}

// ProvisionDeviceResult holds the result of provisioning a single device.
type ProvisionDeviceResult struct {
	Device  string
	Applied int
	Err     error
}

// ProvisionResult holds the aggregate result of a provisioning operation.
type ProvisionResult struct {
	Results []ProvisionDeviceResult
}

// ============================================================================
// Read Response Types
// ============================================================================

// DeviceInfo is a structured snapshot of device state.
type DeviceInfo struct {
	Name             string   `json:"name"`
	MgmtIP           string   `json:"mgmt_ip"`
	LoopbackIP       string   `json:"loopback_ip"`
	Platform         string   `json:"platform"`
	Zone             string   `json:"zone"`
	BGPAS            int      `json:"bgp_as"`
	RouterID         string   `json:"router_id"`
	VTEPSourceIP     string   `json:"vtep_source_ip"`
	BGPNeighbors     []string `json:"bgp_neighbors"`
	InterfaceCount   int      `json:"interfaces"`
	PortChannelCount int      `json:"port_channels"`
	VLANCount        int      `json:"vlans"`
	VRFCount         int      `json:"vrfs"`
}

// InterfaceSummary is a row in interface list output.
type InterfaceSummary struct {
	Name        string   `json:"name"`
	AdminStatus string   `json:"admin_status"`
	OperStatus  string   `json:"oper_status"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
	VRF         string   `json:"vrf,omitempty"`
	Service     string   `json:"service,omitempty"`
}

// InterfaceDetail is all properties of a single interface.
type InterfaceDetail struct {
	Name        string   `json:"name"`
	AdminStatus string   `json:"admin_status"`
	OperStatus  string   `json:"oper_status"`
	Speed       string   `json:"speed"`
	MTU         int      `json:"mtu"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
	VRF         string   `json:"vrf,omitempty"`
	Service     string   `json:"service,omitempty"`
	PCMember    bool     `json:"pc_member"`
	PCParent    string   `json:"pc_parent,omitempty"`
	IngressACL  string   `json:"ingress_acl,omitempty"`
	EgressACL   string   `json:"egress_acl,omitempty"`
	PCMembers   []string `json:"pc_members,omitempty"`
	VLANMembers []string `json:"vlan_members,omitempty"`
}

// BGPNeighborEntry is a BGP neighbor from CONFIG_DB for VRF show.
type BGPNeighborEntry struct {
	Address     string `json:"address"`
	ASN         string `json:"asn"`
	Description string `json:"description,omitempty"`
}

// VRFDetail is VRF info plus BGP neighbors from CONFIG_DB.
type VRFDetail struct {
	Name         string             `json:"name"`
	L3VNI        int                `json:"l3_vni,omitempty"`
	Interfaces   []string           `json:"interfaces,omitempty"`
	BGPNeighbors []BGPNeighborEntry `json:"bgp_neighbors,omitempty"`
}

// VRFStatusEntry is a VRF with operational state from STATE_DB.
type VRFStatusEntry struct {
	Name       string `json:"name"`
	L3VNI      int    `json:"l3_vni,omitempty"`
	Interfaces int    `json:"interfaces"`
	State      string `json:"state,omitempty"`
}

// ACLTableSummary is a row in ACL list output.
type ACLTableSummary struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Stage      string `json:"stage"`
	Interfaces string `json:"interfaces"`
	RuleCount  int    `json:"rule_count"`
}

// ACLRuleInfo is a single ACL rule.
type ACLRuleInfo struct {
	Name     string `json:"name"`
	Priority string `json:"priority"`
	Action   string `json:"action"`
	SrcIP    string `json:"src_ip,omitempty"`
	DstIP    string `json:"dst_ip,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	SrcPort  string `json:"src_port,omitempty"`
	DstPort  string `json:"dst_port,omitempty"`
}

// ACLTableDetail is an ACL table with all its rules.
type ACLTableDetail struct {
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Stage       string        `json:"stage"`
	Interfaces  string        `json:"interfaces"`
	Description string        `json:"description,omitempty"`
	Rules       []ACLRuleInfo `json:"rules"`
}

// BGPNeighborStatus is a BGP neighbor with config + operational state.
type BGPNeighborStatus struct {
	Address   string `json:"address"`
	VRF       string `json:"vrf,omitempty"`
	Type      string `json:"type"`
	RemoteAS  string `json:"remote_as"`
	LocalAddr string `json:"local_addr,omitempty"`
	Admin     string `json:"admin_status"`
	Name      string `json:"description,omitempty"`
	State     string `json:"state,omitempty"`
	PfxRcvd   string `json:"pfx_rcvd,omitempty"`
	PfxSent   string `json:"pfx_sent,omitempty"`
	Uptime    string `json:"uptime,omitempty"`
}

// BGPStatusResult is the complete BGP status view.
type BGPStatusResult struct {
	LocalAS    int                 `json:"local_as"`
	RouterID   string              `json:"router_id"`
	LoopbackIP string              `json:"loopback_ip"`
	Neighbors  []BGPNeighborStatus `json:"neighbors,omitempty"`
	EVPNPeers  []string            `json:"evpn_peers,omitempty"`
}

// VNIMapping is a VNI to VLAN/VRF mapping.
type VNIMapping struct {
	VNI      string `json:"vni"`
	Type     string `json:"type"`
	Resource string `json:"resource"`
}

// L3VNIEntry is a VRF with its L3VNI.
type L3VNIEntry struct {
	VRF   string `json:"vrf"`
	L3VNI int    `json:"l3vni"`
}

// EVPNStatusResult is the complete EVPN status view.
type EVPNStatusResult struct {
	VTEPs       map[string]string `json:"vteps,omitempty"`
	NVOs        map[string]string `json:"nvos,omitempty"`
	VNIMappings []VNIMapping      `json:"vni_mappings,omitempty"`
	L3VNIVRFs   []L3VNIEntry      `json:"l3vni_vrfs,omitempty"`
	VTEPStatus  string            `json:"vtep_status,omitempty"`
	RemoteVTEPs []string          `json:"remote_vteps,omitempty"`
	VNICount    int               `json:"vni_count"`
}

// ServiceBindingDetail is the full service binding on an interface.
type ServiceBindingDetail struct {
	Service     string   `json:"service"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
	VRF         string   `json:"vrf,omitempty"`
}

// LAGStatusEntry is a LAG with operational state.
type LAGStatusEntry struct {
	Name          string   `json:"name"`
	AdminStatus   string   `json:"admin_status"`
	OperStatus    string   `json:"oper_status,omitempty"`
	Members       []string `json:"members"`
	ActiveMembers []string `json:"active_members"`
	MTU           int      `json:"mtu,omitempty"`
}

// VLANStatusEntry is a VLAN with summary details for status/list views.
type VLANStatusEntry struct {
	ID          int               `json:"id"`
	Name        string            `json:"name,omitempty"`
	L2VNI       int               `json:"l2_vni,omitempty"`
	SVI         string            `json:"svi,omitempty"`
	MemberCount int               `json:"member_count"`
	MemberNames []string          `json:"members,omitempty"`
	MACVPN      string            `json:"macvpn,omitempty"`
	MACVPNInfo  *VLANMACVPNDetail `json:"macvpn_detail,omitempty"`
}

// VLANMACVPNDetail holds MAC-VPN binding details for a VLAN.
type VLANMACVPNDetail struct {
	Name           string `json:"name,omitempty"`
	L2VNI          int    `json:"l2_vni,omitempty"`
	ARPSuppression bool   `json:"arp_suppression"`
}

// ============================================================================
// Health Types
// ============================================================================

// HealthReport is the complete health status for a device.
type HealthReport struct {
	Device      string              `json:"device"`
	Status      string              `json:"status"` // "healthy", "degraded", "unhealthy"
	ConfigCheck *VerificationResult `json:"config_check,omitempty"`
	OperChecks  []HealthCheckResult `json:"oper_checks,omitempty"`
}

// HealthCheckResult represents the result of a single operational health check.
type HealthCheckResult struct {
	Check   string `json:"check"`   // Check name (e.g., "bgp", "interface-oper")
	Status  string `json:"status"`  // "pass", "warn", "fail"
	Message string `json:"message"` // Human-readable message
}

// ============================================================================
// Composite Types
// ============================================================================

// CompositeInfo holds metadata about a generated composite config.
type CompositeInfo struct {
	DeviceName string         `json:"device_name"`
	EntryCount int            `json:"entry_count"`
	Tables     map[string]int `json:"tables"` // table name → entry count
	internal   any            // opaque reference to the underlying CompositeConfig
}

// CompositeMode defines the delivery mode for composite configs.
type CompositeMode string

const (
	// CompositeOverwrite merges composite entries on top of existing CONFIG_DB,
	// preserving factory defaults. Only stale keys are removed.
	CompositeOverwrite CompositeMode = "overwrite"

	// CompositeMerge adds entries to existing CONFIG_DB.
	CompositeMerge CompositeMode = "merge"
)

// DeliveryResult reports the outcome of delivering a composite config.
type DeliveryResult struct {
	Applied int `json:"applied"` // Number of entries written
	Skipped int `json:"skipped"` // Number of entries skipped
	Failed  int `json:"failed"`  // Number of entries that failed
}

// ============================================================================
// Spec Detail Types (API view of spec objects)
// ============================================================================

// ServiceDetail is the API view of a service definition.
type ServiceDetail struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	ServiceType   string `json:"service_type"`
	IPVPN         string `json:"ipvpn,omitempty"`
	MACVPN        string `json:"macvpn,omitempty"`
	VRFType       string `json:"vrf_type,omitempty"`
	QoSPolicy     string `json:"qos_policy,omitempty"`
	IngressFilter string `json:"ingress_filter,omitempty"`
	EgressFilter  string `json:"egress_filter,omitempty"`
}

// IPVPNDetail is the API view of an IP-VPN definition.
type IPVPNDetail struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	VRF          string   `json:"vrf"`
	L3VNI        int      `json:"l3vni"`
	RouteTargets []string `json:"route_targets"`
}

// MACVPNDetail is the API view of a MAC-VPN definition.
type MACVPNDetail struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	AnycastIP      string   `json:"anycast_ip,omitempty"`
	AnycastMAC     string   `json:"anycast_mac,omitempty"`
	VNI            int      `json:"vni"`
	VlanID         int      `json:"vlan_id"`
	RouteTargets   []string `json:"route_targets,omitempty"`
	ARPSuppression bool     `json:"arp_suppression,omitempty"`
}

// QoSPolicyDetail is the API view of a QoS policy.
type QoSPolicyDetail struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Queues      []QoSQueueEntry `json:"queues"`
}

// QoSQueueEntry is a single queue in a QoS policy.
type QoSQueueEntry struct {
	QueueID int    `json:"queue_id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Weight  int    `json:"weight,omitempty"`
	DSCP    []int  `json:"dscp,omitempty"`
	ECN     bool   `json:"ecn,omitempty"`
}

// FilterDetail is the API view of a filter definition.
type FilterDetail struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type"`
	Rules       []FilterRuleEntry `json:"rules"`
}

// FilterRuleEntry is a single rule in a filter.
type FilterRuleEntry struct {
	Sequence      int    `json:"seq"`
	Action        string `json:"action"`
	SrcIP         string `json:"src_ip,omitempty"`
	DstIP         string `json:"dst_ip,omitempty"`
	SrcPrefixList string `json:"src_prefix_list,omitempty"`
	DstPrefixList string `json:"dst_prefix_list,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	SrcPort       string `json:"src_port,omitempty"`
	DstPort       string `json:"dst_port,omitempty"`
	DSCP          string `json:"dscp,omitempty"`
	CoS           string `json:"cos,omitempty"`
	Log           bool   `json:"log,omitempty"`
}

// PlatformDetail is the API view of a platform definition.
type PlatformDetail struct {
	Name                string   `json:"name"`
	HWSKU               string   `json:"hwsku"`
	Description         string   `json:"description,omitempty"`
	DeviceType          string   `json:"device_type,omitempty"`
	Dataplane           string   `json:"dataplane,omitempty"`
	DefaultSpeed        string   `json:"default_speed"`
	PortCount           int      `json:"port_count"`
	Breakouts           []string `json:"breakouts,omitempty"`
	UnsupportedFeatures []string `json:"unsupported_features,omitempty"`
}

// ============================================================================
// Spec Authoring Request Types
// ============================================================================

// CreateServiceRequest is the request for creating a service definition.
type CreateServiceRequest struct {
	Name          string
	Type          string
	IPVPN         string
	MACVPN        string
	VRFType       string
	QoSPolicy     string
	IngressFilter string
	EgressFilter  string
	Description   string
}

// CreateIPVPNRequest is the request for creating an IP-VPN definition.
type CreateIPVPNRequest struct {
	Name         string
	L3VNI        int
	VRF          string
	RouteTargets []string
	Description  string
}

// CreateMACVPNRequest is the request for creating a MAC-VPN definition.
type CreateMACVPNRequest struct {
	Name           string
	VNI            int
	VlanID         int
	AnycastIP      string
	AnycastMAC     string
	RouteTargets   []string
	ARPSuppression bool
	Description    string
}

// CreateQoSPolicyRequest is the request for creating a QoS policy.
type CreateQoSPolicyRequest struct {
	Name        string
	Description string
}

// AddQoSQueueRequest is the request for adding a queue to a QoS policy.
type AddQoSQueueRequest struct {
	Policy  string
	QueueID int
	Name    string
	Type    string
	Weight  int
	DSCP    []int
	ECN     bool
}

// RemoveQoSQueueRequest is the request for removing a queue from a QoS policy.
type RemoveQoSQueueRequest struct {
	Policy  string
	QueueID int
}

// CreateFilterRequest is the request for creating a filter definition.
type CreateFilterRequest struct {
	Name        string
	Type        string
	Description string
}

// AddFilterRuleRequest is the request for adding a rule to a filter.
type AddFilterRuleRequest struct {
	Filter        string
	Sequence      int
	Action        string
	SrcIP         string
	DstIP         string
	SrcPrefixList string
	DstPrefixList string
	Protocol      string
	SrcPort       string
	DstPort       string
	DSCP          string
	CoS           string
	Log           bool
}

// RemoveFilterRuleRequest is the request for removing a rule from a filter.
type RemoveFilterRuleRequest struct {
	Filter   string
	Priority int
}

// ============================================================================
// Audit Types
// ============================================================================

// AuditFilter defines criteria for querying audit events.
type AuditFilter struct {
	Device      string
	User        string
	Operation   string
	Service     string
	Interface   string
	StartTime   time.Time
	EndTime     time.Time
	Limit       int
	Offset      int
	SuccessOnly bool
	FailureOnly bool
}

// AuditEvent represents an auditable configuration change event.
type AuditEvent struct {
	ID          string      `json:"id"`
	Timestamp   string      `json:"timestamp"`
	User        string      `json:"user"`
	Device      string      `json:"device"`
	Operation   string      `json:"operation"`
	Service     string      `json:"service,omitempty"`
	Interface   string      `json:"interface,omitempty"`
	Changes     []AuditChange `json:"changes"`
	Success     bool        `json:"success"`
	Error       string      `json:"error,omitempty"`
	ExecuteMode bool        `json:"execute_mode"`
	DryRun      bool        `json:"dry_run"`
	Duration    string      `json:"duration"`
	ClientIP    string      `json:"client_ip,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
}

// AuditChange is a single CONFIG_DB change within an audit event.
type AuditChange struct {
	Table  string            `json:"table"`
	Key    string            `json:"key"`
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields,omitempty"`
}

// ============================================================================
// Settings Types
// ============================================================================

// UserSettings holds persistent user preferences.
type UserSettings struct {
	DefaultNetwork  string `json:"default_network,omitempty"`
	SpecDir         string `json:"spec_dir,omitempty"`
	DefaultSuite    string `json:"default_suite,omitempty"`
	TopologiesDir   string `json:"topologies_dir,omitempty"`
	AuditLogPath    string `json:"audit_log_path,omitempty"`
	AuditMaxSizeMB  int    `json:"audit_max_size_mb,omitempty"`
	AuditMaxBackups int    `json:"audit_max_backups,omitempty"`
}

// DefaultSpecDir is the default specification directory.
const DefaultSpecDir = "/etc/newtron"

// DefaultAuditMaxSizeMB is the default maximum audit log size in megabytes.
const DefaultAuditMaxSizeMB = 10

// DefaultAuditMaxBackups is the default maximum number of rotated audit log files.
const DefaultAuditMaxBackups = 10

// GetSpecDir returns the spec directory with a fallback default.
func (us *UserSettings) GetSpecDir() string {
	if us.SpecDir != "" {
		return us.SpecDir
	}
	return DefaultSpecDir
}

// GetAuditLogPath returns the audit log path with a fallback default.
func (us *UserSettings) GetAuditLogPath(specDir string) string {
	if us.AuditLogPath != "" {
		return us.AuditLogPath
	}
	if specDir != "" {
		return specDir + "/audit.log"
	}
	return "/var/log/newtron/audit.log"
}

// GetAuditMaxSizeMB returns the audit max size in MB with a default of 10.
func (us *UserSettings) GetAuditMaxSizeMB() int {
	if us.AuditMaxSizeMB > 0 {
		return us.AuditMaxSizeMB
	}
	return DefaultAuditMaxSizeMB
}

// GetAuditMaxBackups returns the audit max backups with a default of 10.
func (us *UserSettings) GetAuditMaxBackups() int {
	if us.AuditMaxBackups > 0 {
		return us.AuditMaxBackups
	}
	return DefaultAuditMaxBackups
}

// ============================================================================
// Route Types
// ============================================================================

// RouteEntry represents a route read from a device's routing table.
type RouteEntry struct {
	Prefix   string         `json:"prefix"`
	VRF      string         `json:"vrf"`
	Protocol string         `json:"protocol"`
	NextHops []RouteNextHop `json:"next_hops,omitempty"`
	Source   string         `json:"source"` // "APP_DB" or "ASIC_DB"
}

// RouteNextHop represents a single next-hop in a route entry.
type RouteNextHop struct {
	Address   string `json:"address"`
	Interface string `json:"interface"`
}
