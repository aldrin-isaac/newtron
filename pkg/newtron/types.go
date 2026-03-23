// Package newtron provides the top-level API for the newtron network automation system.
//
// This file defines all types, constants, request/response structs, and error types
// used by the newtron API layer.
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
	Preview      string              `json:"preview,omitempty"`
	ChangeCount  int                 `json:"change_count"`
	Applied      bool                `json:"applied"`
	Verified     bool                `json:"verified"`
	Saved        bool                `json:"saved"`
	Verification *VerificationResult `json:"verification,omitempty"`
}

// VerificationResult reports ChangeSet verification outcome.
type VerificationResult struct {
	Passed int                 `json:"passed"`
	Failed int                 `json:"failed"`
	Errors []VerificationError `json:"errors,omitempty"`
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
	VRF         string `json:"vrf,omitempty"`
	Interface   string `json:"interface,omitempty"`
	RemoteAS    int    `json:"remote_as,omitempty"`
	NeighborIP  string `json:"neighbor_ip,omitempty"`
	Description string `json:"description,omitempty"`
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
// Device Operation Request Types
// ============================================================================

// CleanupSummary provides details about orphaned resources found and removed.
type CleanupSummary struct {
	OrphanedACLs        []string `json:"orphaned_acls,omitempty"`
	OrphanedVRFs        []string `json:"orphaned_vrfs,omitempty"`
	OrphanedVNIMappings []string `json:"orphaned_vni_mappings,omitempty"`
}

// ============================================================================
// Provision Operation Request/Result Types
// ============================================================================

// ProvisionRequest is the request for provisioning devices.
type ProvisionRequest struct {
	Devices []string `json:"devices,omitempty"` // empty = all devices in topology
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

// NeighEntry represents a neighbor (ARP/NDP) entry read from STATE_DB.
type NeighEntry struct {
	IP        string `json:"ip"`
	Interface string `json:"interface"`
	MAC       string `json:"mac"`
	Family    string `json:"family"` // "IPv4" or "IPv6"
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
// Profile and Zone Detail Types
// ============================================================================

// DeviceProfileDetail is the API view of a device profile.
type DeviceProfileDetail struct {
	Name        string      `json:"name"`
	MgmtIP      string      `json:"mgmt_ip"`
	LoopbackIP  string      `json:"loopback_ip"`
	Zone        string      `json:"zone"`
	Platform    string      `json:"platform,omitempty"`
	MAC         string      `json:"mac,omitempty"`
	UnderlayASN int         `json:"underlay_asn,omitempty"`
	SSHUser     string      `json:"ssh_user,omitempty"`
	SSHPort     int         `json:"ssh_port,omitempty"`
	EVPN        *EVPNDetail `json:"evpn,omitempty"`
}

// EVPNDetail is the API view of EVPN peering config within a profile.
type EVPNDetail struct {
	Peers          []string `json:"peers,omitempty"`
	RouteReflector bool     `json:"route_reflector,omitempty"`
	ClusterID      string   `json:"cluster_id,omitempty"`
}

// RouteReflectorPeer describes a BGP peer for route reflector configuration.
type RouteReflectorPeer struct {
	IP  string `json:"ip"`
	ASN int    `json:"asn"`
}

// RouteReflectorOpts holds configuration for ConfigureRouteReflector.
type RouteReflectorOpts struct {
	ClusterID string               `json:"cluster_id"`
	LocalASN  int                  `json:"local_asn"`
	RouterID  string               `json:"router_id"`
	LocalAddr string               `json:"local_addr"`
	Clients   []RouteReflectorPeer `json:"clients"`
	Peers     []RouteReflectorPeer `json:"peers"`
}

// ZoneDetail is the API view of a zone definition.
type ZoneDetail struct {
	Name string `json:"name"`
}

// CreateDeviceProfileRequest is the request for creating a device profile.
type CreateDeviceProfileRequest struct {
	Name        string                  `json:"name"`
	MgmtIP      string                  `json:"mgmt_ip"`
	LoopbackIP  string                  `json:"loopback_ip"`
	Zone        string                  `json:"zone"`
	Platform    string                  `json:"platform,omitempty"`
	MAC         string                  `json:"mac,omitempty"`
	UnderlayASN int                     `json:"underlay_asn,omitempty"`
	SSHUser     string                  `json:"ssh_user,omitempty"`
	SSHPass     string                  `json:"ssh_pass,omitempty"`
	SSHPort     int                     `json:"ssh_port,omitempty"`
	EVPN        *CreateEVPNConfigRequest `json:"evpn,omitempty"`
}

// CreateEVPNConfigRequest defines EVPN peering for profile creation.
type CreateEVPNConfigRequest struct {
	Peers          []string `json:"peers,omitempty"`
	RouteReflector bool     `json:"route_reflector,omitempty"`
	ClusterID      string   `json:"cluster_id,omitempty"`
}

// CreateZoneRequest is the request for creating a zone.
type CreateZoneRequest struct {
	Name string `json:"name"`
}

// ============================================================================
// Spec Authoring Request Types
// ============================================================================

// CreateServiceRequest is the request for creating a service definition.
type CreateServiceRequest struct {
	Name          string                `json:"name"`
	Type          string                `json:"type"`
	IPVPN         string                `json:"ipvpn,omitempty"`
	MACVPN        string                `json:"macvpn,omitempty"`
	VRFType       string                `json:"vrf_type,omitempty"`
	QoSPolicy     string                `json:"qos_policy,omitempty"`
	IngressFilter string                `json:"ingress_filter,omitempty"`
	EgressFilter  string                `json:"egress_filter,omitempty"`
	Description   string                `json:"description,omitempty"`
	Routing       *CreateServiceRouting `json:"routing,omitempty"`
}

// CreateServiceRouting defines routing parameters for service creation.
type CreateServiceRouting struct {
	Protocol         string `json:"protocol"`
	PeerAS           string `json:"peer_as,omitempty"`
	ImportPolicy     string `json:"import_policy,omitempty"`
	ExportPolicy     string `json:"export_policy,omitempty"`
	ImportCommunity  string `json:"import_community,omitempty"`
	ExportCommunity  string `json:"export_community,omitempty"`
	ImportPrefixList string `json:"import_prefix_list,omitempty"`
	ExportPrefixList string `json:"export_prefix_list,omitempty"`
	Redistribute     *bool  `json:"redistribute,omitempty"`
}

// CreateIPVPNRequest is the request for creating an IP-VPN definition.
type CreateIPVPNRequest struct {
	Name         string   `json:"name"`
	L3VNI        int      `json:"l3vni"`
	VRF          string   `json:"vrf,omitempty"`
	RouteTargets []string `json:"route_targets,omitempty"`
	Description  string   `json:"description,omitempty"`
}

// CreateMACVPNRequest is the request for creating a MAC-VPN definition.
type CreateMACVPNRequest struct {
	Name           string   `json:"name"`
	VNI            int      `json:"vni"`
	VlanID         int      `json:"vlan_id,omitempty"`
	AnycastIP      string   `json:"anycast_ip,omitempty"`
	AnycastMAC     string   `json:"anycast_mac,omitempty"`
	RouteTargets   []string `json:"route_targets,omitempty"`
	ARPSuppression bool     `json:"arp_suppression,omitempty"`
	Description    string   `json:"description,omitempty"`
}

// CreateQoSPolicyRequest is the request for creating a QoS policy.
type CreateQoSPolicyRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AddQoSQueueRequest is the request for adding a queue to a QoS policy.
type AddQoSQueueRequest struct {
	Policy  string `json:"policy"`
	QueueID int    `json:"queue_id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Weight  int    `json:"weight,omitempty"`
	DSCP    []int  `json:"dscp,omitempty"`
	ECN     bool   `json:"ecn,omitempty"`
}

// CreateFilterRequest is the request for creating a filter definition.
type CreateFilterRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// AddFilterRuleRequest is the request for adding a rule to a filter.
type AddFilterRuleRequest struct {
	Filter        string `json:"filter"`
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
}

// CreatePrefixListRequest is the request for creating a prefix list.
type CreatePrefixListRequest struct {
	Name     string   `json:"name"`
	Prefixes []string `json:"prefixes,omitempty"`
}

// AddPrefixListEntryRequest is the request for adding an entry to a prefix list.
type AddPrefixListEntryRequest struct {
	PrefixList string `json:"prefix_list"`
	Prefix     string `json:"prefix"`
}

// PrefixListDetail is the detail response for a prefix list.
type PrefixListDetail struct {
	Name     string   `json:"name"`
	Prefixes []string `json:"prefixes"`
}

// CreateRoutePolicyRequest is the request for creating a route policy.
type CreateRoutePolicyRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AddRoutePolicyRuleRequest is the request for adding a rule to a route policy.
type AddRoutePolicyRuleRequest struct {
	Policy     string              `json:"policy"`
	Sequence   int                 `json:"seq"`
	Action     string              `json:"action"`
	PrefixList string              `json:"prefix_list,omitempty"`
	Community  string              `json:"community,omitempty"`
	Set        *RoutePolicySetSpec `json:"set,omitempty"`
}

// RoutePolicySetSpec defines set-actions in a route policy rule (API-level type).
type RoutePolicySetSpec struct {
	LocalPref int    `json:"local_pref,omitempty"`
	Community string `json:"community,omitempty"`
	MED       int    `json:"med,omitempty"`
}

// RoutePolicyDetail is the detail response for a route policy.
type RoutePolicyDetail struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Rules       []RoutePolicyRuleEntry `json:"rules"`
}

// RoutePolicyRuleEntry is a single rule in a RoutePolicyDetail.
type RoutePolicyRuleEntry struct {
	Sequence   int                 `json:"seq"`
	Action     string              `json:"action"`
	PrefixList string              `json:"prefix_list,omitempty"`
	Community  string              `json:"community,omitempty"`
	Set        *RoutePolicySetSpec `json:"set,omitempty"`
}

// ============================================================================
// Intent Types — Unified Intent Model (§39)
// ============================================================================

// IntentState represents the lifecycle state of an intent.
type IntentState string

const (
	// IntentUnrealized means the intent has been declared but not yet
	// applied to the device. Topology imports create unrealized intents.
	IntentUnrealized IntentState = "unrealized"

	// IntentInFlight means actuation has begun — CONFIG_DB writes are
	// in progress. If the process crashes in this state, the intent is
	// a zombie (crash recovery via rollback or force-complete).
	IntentInFlight IntentState = "in-flight"

	// IntentActuated means all operations completed successfully and
	// the intent is fully realized on the device.
	IntentActuated IntentState = "actuated"
)

// Intent is the fundamental unit of the domain model. It binds a desired
// state to a device resource — an interface, a subsystem (BGP, EVPN), or
// a device-wide concern (loopback, baseline).
//
// An intent is a composite of primitives: ApplyService expands into
// CreateVLAN + CreateVRF + AddBGPNeighbor + ... Each primitive is tracked
// in the Operations list for crash recovery and rollback.
//
// The same Intent type is used in all contexts:
//   - Abstract nodes (offline): populated from topology.json imports
//   - Connected nodes (online): loaded from CONFIG_DB on connect
//   - Both: updated on mutations, exportable to file/API
//
// Params carry resolved values sufficient for both teardown and
// reconstruction (§37). The reverse path never re-resolves specs —
// it works from Params alone.
type Intent struct {
	// Identity — what resource this intent is bound to and what it does.
	// Resource is the binding point: "Ethernet0", "bgp", "evpn", "loopback".
	// Operation is the composite: "apply-service", "setup-evpn", "configure-bgp".
	// Name references the spec that parameterizes the operation (e.g., "transit").
	// Empty Name means the operation needs no spec reference.
	Resource  string `json:"resource"`
	Operation string `json:"operation"`
	Name      string `json:"name,omitempty"`

	// Lifecycle
	State   IntentState `json:"state"`
	Holder  string      `json:"holder,omitempty"`  // who created/is actuating
	Created time.Time   `json:"created,omitempty"` // when actuation started

	// Audit — set when actuation completes.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	AppliedBy string     `json:"applied_by,omitempty"`

	// Resolved parameters — concrete values for reconstruction + teardown.
	// What the operation resolved to when applied. Self-sufficient: the
	// reverse operation and drift reconstruction can work from Params alone
	// without re-resolving specs (§37).
	Params map[string]string `json:"params,omitempty"`

	// Composite operations — the expanded primitive list.
	// Written before actuation begins (crash-safe from that point).
	// Each primitive tracks completion status for partial rollback.
	Phase           string            `json:"phase,omitempty"`           // "" (applying) or "rolling_back"
	RollbackHolder  string            `json:"rollback_holder,omitempty"` // who is rolling back
	RollbackStarted *time.Time        `json:"rollback_started,omitempty"`
	Operations      []IntentOperation `json:"operations,omitempty"`
}

// IntentOperation represents a single primitive within a composite intent.
// Each operation tracks its own lifecycle (started/completed/reversed)
// for crash recovery — if the process dies mid-actuation, retry skips
// already-completed operations and continues where it left off.
type IntentOperation struct {
	Name      string            `json:"name"`
	Params    map[string]string `json:"params,omitempty"`
	ReverseOp string            `json:"reverse_op,omitempty"`
	Started   *time.Time        `json:"started,omitempty"`
	Completed *time.Time        `json:"completed,omitempty"`
	Reversed  *time.Time        `json:"reversed,omitempty"`
}

// OperationIntent is the crash-recovery record stored in CONFIG_DB
// (NEWTRON_INTENT table). This is the persistence adapter for the
// in-flight subset of the Intent model.
//
// Written before Commit applies ChangeSets, deleted on success.
// If the process crashes, the intent remains for the operator to
// inspect and roll back via 'device zombie'.
//
// During rollback, Phase transitions to "rolling_back". Each operation
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

// IsService returns true if this intent represents a service binding.
func (i *Intent) IsService() bool {
	return i.Operation == "apply-service"
}

// IsInFlight returns true if this intent is currently being actuated.
func (i *Intent) IsInFlight() bool {
	return i.State == IntentInFlight
}

// IsActuated returns true if this intent has been fully realized on the device.
func (i *Intent) IsActuated() bool {
	return i.State == IntentActuated
}

// ToOperationIntent converts the in-flight aspects of an Intent to the
// CONFIG_DB persistence format (OperationIntent). Used when writing the
// crash-recovery record to NEWTRON_INTENT.
func (i *Intent) ToOperationIntent() *OperationIntent {
	return &OperationIntent{
		Holder:          i.Holder,
		Created:         i.Created,
		Phase:           i.Phase,
		RollbackHolder:  i.RollbackHolder,
		RollbackStarted: i.RollbackStarted,
		Operations:      i.Operations,
	}
}

// IntentFromOperationIntent creates an Intent from a CONFIG_DB
// OperationIntent record (crash recovery). The resulting intent
// is in-flight state — it was found on-device after a crash.
func IntentFromOperationIntent(resource string, oi *OperationIntent) *Intent {
	return &Intent{
		Resource:        resource,
		Operation:       "commit", // generic — the operations list carries specifics
		State:           IntentInFlight,
		Holder:          oi.Holder,
		Created:         oi.Created,
		Phase:           oi.Phase,
		RollbackHolder:  oi.RollbackHolder,
		RollbackStarted: oi.RollbackStarted,
		Operations:      oi.Operations,
	}
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
	ServerURL       string `json:"server_url,omitempty"`
	NetworkID       string `json:"network_id,omitempty"`
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

// DefaultServerURL is the default newtron-server address.
const DefaultServerURL = "http://localhost:8080"

// DefaultNetworkID is the default network identifier.
const DefaultNetworkID = "default"

// GetServerURL returns the server URL with a fallback default.
func (us *UserSettings) GetServerURL() string {
	if us.ServerURL != "" {
		return us.ServerURL
	}
	return DefaultServerURL
}

// GetNetworkID returns the network ID with a fallback default.
func (us *UserSettings) GetNetworkID() string {
	if us.NetworkID != "" {
		return us.NetworkID
	}
	return DefaultNetworkID
}

// ============================================================================
// History Types (rolling operation history for rollback)
// ============================================================================

// HistoryEntry represents a completed commit archived for rollback.
type HistoryEntry struct {
	Sequence   int               `json:"sequence"`
	Holder     string            `json:"holder"`
	Timestamp  time.Time         `json:"timestamp"`
	Operations []IntentOperation `json:"operations"`
}

// HistoryResult wraps the rolling history for API responses.
type HistoryResult struct {
	Device  string         `json:"device"`
	Entries []HistoryEntry `json:"entries"`
}

// HistoryRollbackResult reports the outcome of a history rollback.
type HistoryRollbackResult struct {
	RolledBack *HistoryRollbackEntry `json:"rolled_back,omitempty"`
}

// HistoryRollbackEntry identifies the rolled-back history entry.
type HistoryRollbackEntry struct {
	Sequence           int `json:"sequence"`
	OperationsReversed int `json:"operations_reversed"`
}

// ============================================================================
// Drift Detection Types
// ============================================================================

// DriftEntry describes a single difference between expected and actual CONFIG_DB.
type DriftEntry struct {
	Table    string            `json:"table"`
	Key      string            `json:"key"`
	Type     string            `json:"type"` // "missing", "extra", "modified"
	Expected map[string]string `json:"expected,omitempty"`
	Actual   map[string]string `json:"actual,omitempty"`
}

// DriftReport is the complete drift detection result for a device.
type DriftReport struct {
	Device   string       `json:"device"`
	Status   string       `json:"status"` // "clean" or "drifted"
	Missing  []DriftEntry `json:"missing,omitempty"`
	Extra    []DriftEntry `json:"extra,omitempty"`
	Modified []DriftEntry `json:"modified,omitempty"`
}

// NetworkDriftSummary is the per-device drift status for network-level views.
type NetworkDriftSummary struct {
	Devices []DeviceDriftStatus `json:"devices"`
}

// DeviceDriftStatus is a single device's drift summary.
type DeviceDriftStatus struct {
	Device   string `json:"device"`
	Status   string `json:"status"`
	Missing  int    `json:"missing,omitempty"`
	Extra    int    `json:"extra,omitempty"`
	Modified int    `json:"modified,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ============================================================================
// Device Settings
// ============================================================================

// DeviceSettings holds per-device newtron operational tuning stored in CONFIG_DB.
type DeviceSettings struct {
	MaxHistory int `json:"max_history"`
}

// ============================================================================
// Host Types
// ============================================================================

// HostProfile contains SSH connection parameters for a virtual host device.
type HostProfile struct {
	MgmtIP  string `json:"mgmt_ip"`
	SSHUser string `json:"ssh_user"`
	SSHPass string `json:"ssh_pass"`
	SSHPort int    `json:"ssh_port"`
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

// ============================================================================
// Request types used by the HTTP client and server.
// These live in the public API package so that consumers (CLI, newtrun) do not
// need to import the internal server package (pkg/newtron/api).
// ============================================================================

// SVIConfigureRequest is the request body for configuring an SVI.
type SVIConfigureRequest struct {
	VlanID     int    `json:"vlan_id"`
	VRF        string `json:"vrf,omitempty"`
	IPAddress  string `json:"ip_address,omitempty"`
	AnycastMAC string `json:"anycast_mac,omitempty"`
}

// ACLCreateRequest is the request body for creating an ACL table.
type ACLCreateRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Stage       string `json:"stage"`
	Ports       string `json:"ports,omitempty"`
	Description string `json:"description,omitempty"`
}

// ACLRuleAddRequest is the request body for adding a rule to an ACL table.
type ACLRuleAddRequest struct {
	RuleName string `json:"rule_name"`
	Priority int    `json:"priority"`
	Action   string `json:"action"`
	SrcIP    string `json:"src_ip,omitempty"`
	DstIP    string `json:"dst_ip,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	SrcPort  string `json:"src_port,omitempty"`
	DstPort  string `json:"dst_port,omitempty"`
}

// PortChannelCreateRequest is the request body for creating a port channel.
type PortChannelCreateRequest struct {
	Name     string   `json:"name"`
	Members  []string `json:"members,omitempty"`
	MinLinks int      `json:"min_links,omitempty"`
	FastRate bool     `json:"fast_rate,omitempty"`
	Fallback bool     `json:"fallback,omitempty"`
	MTU      int      `json:"mtu,omitempty"`
}
