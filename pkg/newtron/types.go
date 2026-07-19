// Package newtron provides the top-level API for the newtron network automation system.
//
// This file defines all types, constants, request/response structs, and error types
// used by the newtron API layer.
package newtron

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ============================================================================
// Service Type Constants
// ============================================================================

const (
	ServiceTypeEVPNIRB     = "evpn-irb"     // L2+L3 overlay: requires ipvpn + macvpn
	ServiceTypeEVPNBridged = "evpn-bridged" // L2 overlay: requires macvpn
	ServiceTypeEVPNRouted  = "evpn-routed"  // L3 overlay: requires ipvpn
	ServiceTypeIRB         = "irb"          // Local L2+L3: vlan + ip at apply time
	ServiceTypeBridged     = "bridged"      // Local L2: vlan at apply time
	ServiceTypeRouted      = "routed"       // Local L3: ip at apply time
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
//
// Changes is the typed ChangeSet substrate per §46 — every CONFIG_DB add /
// modify / delete the operation produced, in the same `sonic.ConfigChange`
// shape used internally. Preview is the human-readable rendering of the same
// substrate; Changes is the canonical form on the wire.
//
// DeviceOps records the per-substrate-operation outcomes — one entry per
// Redis HSET/DEL during Apply, one verify_read entry per Change during
// Verify. This is the substrate that operator-philosophy invariant #1
// (no black boxes) operationalizes through the Concrete success vision:
// the operator sees exactly which Redis command landed, what the device
// returned verbatim, and which was rejected. §11 + §46.
type WriteResult struct {
	Preview      string               `json:"preview,omitempty"`
	Changes      []sonic.ConfigChange `json:"changes,omitempty"`
	DeviceOps    []sonic.DeviceOp     `json:"device_ops,omitempty"`
	ChangeCount  int                  `json:"change_count"`
	Applied      bool                 `json:"applied"`
	Verified     bool                 `json:"verified"`
	Saved        bool                 `json:"saved"`
	Verification *VerificationResult  `json:"verification,omitempty"`
}

// VerificationResult reports ChangeSet verification outcome.
type VerificationResult struct {
	Passed int                 `json:"passed"`
	Failed int                 `json:"failed"`
	Errors []VerificationError `json:"errors,omitempty"`
}

// VerificationError describes a single verification failure.
//
// DeviceResponse carries the verbatim device-side reply observed at the
// moment the mismatch was detected (per §46). For field mismatches it is the
// full HGETALL content formatted as `key=value` pairs sorted alphabetically;
// for missing-key or still-present cases it is the verbatim Redis-level
// status. Absent when verification ran without device transport.
type VerificationError struct {
	Table          string `json:"table"`
	Key            string `json:"key"`
	Field          string `json:"field"`
	Expected       string `json:"expected"`
	Actual         string `json:"actual"` // "" if missing
	DeviceResponse string `json:"device_response,omitempty"`
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

// ConflictError is re-exported from pkg/util so the parent newtron public
// API and the internal network/spec layers can share a single type. Used
// when a requested mutation would violate an invariant due to references
// from other entities (see DESIGN_PRINCIPLES §15 — cascade is explicit).
type ConflictError = util.ConflictError

// WriteControlError is returned (→ HTTP 409) when a network's write-control
// reservation refuses a request: a mutating write attempted by a caller who is
// not the current holder, or a control request when another caller holds it.
// Holder is "" when enforcement is on but nobody holds control (a write must
// request control first). The structured fields let a client render "alice holds
// write control since 14:02 (last active 14:47) — release, take over, or wait"
// without parsing the message.
type WriteControlError struct {
	Network    string    `json:"network"`
	Holder     string    `json:"holder"`
	Since      time.Time `json:"since"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastActive time.Time `json:"last_active"`
}

func (e *WriteControlError) Error() string {
	if e.Holder == "" {
		return fmt.Sprintf("network %q enforces write control but nobody holds it — request control first", e.Network)
	}
	return fmt.Sprintf("write control of network %q is held by %q until %s (since %s, last active %s)",
		e.Network, e.Holder, e.ExpiresAt.Format(time.RFC3339), e.Since.Format(time.RFC3339), e.LastActive.Format(time.RFC3339))
}

// VerificationFailedError indicates post-apply verification failed.
//
// Result carries the typed WriteResult — including DeviceOps, Verification.Errors
// (with DeviceResponse), Changes — so the wire envelope on 409 responses surfaces
// the full substrate that newtron computed during verify, not just a stringified
// summary. This honors §46 (HTTP API Boundary — Wire Shape Mirrors Canonical Types)
// on the failure path: the typed substrate survives to the consumer.
//
// Set by Node.Commit when verification fails. Always non-nil when this error is
// returned; callers can read Result.Verification, Result.DeviceOps directly.
type VerificationFailedError struct {
	Device  string
	Passed  int
	Failed  int
	Total   int
	Message string
	Result  *WriteResult
}

func (e *VerificationFailedError) Error() string {
	return fmt.Sprintf("verification failed on %s: %d/%d entries did not persist", e.Device, e.Failed, e.Total)
}

// AuthorizationError is the public-API error returned when a
// permission check denies an operation (auth-design.md L3). The
// pkg/newtron/api layer maps this to HTTP 403; the typed payload on
// the response Data field carries the same Caller/Permission/Resource
// fields so a client can render a precise message without parsing
// Error.
//
// Caller is the username the check ran against (the verified identity
// from L1/L2). Permission is the action that was denied
// (e.g. "spec.author"). Resource scopes the denial (e.g. the service
// name being created); empty when the action has no specific
// resource. The original *auth.PermissionError remains in the
// errors chain via Unwrap, so existing util.ErrPermissionDenied
// errors.Is() checks still match.
type AuthorizationError struct {
	Caller     string `json:"caller"`
	Permission string `json:"permission"`
	Resource   string `json:"resource,omitempty"`
	inner      error
}

func (e *AuthorizationError) Error() string {
	msg := fmt.Sprintf("authorization denied: %s lacks %s", e.Caller, e.Permission)
	if e.Resource != "" {
		msg += " on " + e.Resource
	}
	return msg
}

func (e *AuthorizationError) Unwrap() error { return e.inner }

// ============================================================================
// Config Types for Write Operations
// ============================================================================

// VLANConfig holds parameters for creating a VLAN. Identity (the VLAN ID)
// travels as the method argument — one carrier per context; the durable
// trace lives in the intent record the call writes.
type VLANConfig struct {
	Description string
	L2VNI       int
}

// IRBConfig holds parameters for configuring an IRB (Integrated Routing and Bridging) interface.
// Identity (the VLAN ID) travels as the method argument.
type IRBConfig struct {
	VRF        string
	IPAddress  string
	AnycastMAC string
}

// VRFConfig holds parameters for creating a VRF. Identity (the VRF name)
// travels as the method argument; the type is empty today and exists so
// future VRF options land without a signature change (§33).
type VRFConfig struct{}

// BGPNeighborConfig holds parameters for adding a BGP neighbor.
type BGPNeighborConfig struct {
	VRF         string `json:"vrf,omitempty"`
	Interface   string `json:"interface,omitempty"`
	RemoteAS    int    `json:"remote_as,omitempty"`
	NeighborIP  string `json:"neighbor_ip,omitempty"`
	Description string `json:"description,omitempty"`
	Multihop    int    `json:"multihop,omitempty"`
	// EVPN activates the l2vpn evpn address family on the neighbor — the flag
	// add/update-bgp-evpn-peer exist to set. The wire previously dropped it
	// (wrappers hardcoded false), so no wire-created overlay peer could
	// activate the AF; found while authoring the §48 evpn continuity check.
	EVPN bool `json:"evpn,omitempty"`
}

// ACLConfig holds parameters for creating an ACL table. Identity (the table
// name) travels as the method argument.
type ACLConfig struct {
	Type        string
	Stage       string
	Ports       string
	Description string
}

// ACLRuleConfig holds parameters for adding an ACL rule. Identity (table +
// rule name) travels as method arguments (§47 — the composite key is not
// part of the mutable payload).
type ACLRuleConfig struct {
	Priority int
	Action   string
	SrcIP    string
	DstIP    string
	Protocol string
	SrcPort  string
	DstPort  string
}

// InterfaceConfig holds parameters for ConfigureInterface. Routed mode (VRF+IP)
// and bridged mode (VLAN membership) are mutually exclusive.
type InterfaceConfig struct {
	VRF    string // VRF binding (routed mode)
	IP     string // IP address in CIDR notation (routed mode)
	VLAN   int    // VLAN ID (bridged mode)
	Tagged bool   // Tagged membership (bridged mode)
}

// PortChannelConfig holds parameters for creating a LAG/PortChannel.
// Identity (the PortChannel name) travels as the method argument.
type PortChannelConfig struct {
	Members  []string
	MinLinks int
	FastRate bool
	Fallback bool
	MTU      int
}

// ServiceProjectionNode reports the projection-slice contribution of a service
// on a single Node. Diff is the canonical sonic.DriftEntry vocabulary per §11:
// "missing" entries are exclusively the service's contribution; "modified"
// entries are fields the service overlays on top of other intents.
type ServiceProjectionNode struct {
	Node string             `json:"node"`
	Diff []sonic.DriftEntry `json:"diff"`
}

// ServiceProjectionResult aggregates per-Node service slices for the named
// service across every Node newtron currently has built. Nodes that do not
// bind the service are omitted; per-Node ordering is alphabetical by name.
// §11 + §46 — the canonical wire shape for the /service/{name}/projection
// endpoint that operationalizes operator-philosophy invariant #5 (why-mode)
// at the service scope.
type ServiceProjectionResult struct {
	Service string                  `json:"service"`
	Nodes   []ServiceProjectionNode `json:"nodes"`
}

// ApplyServiceOpts contains options for applying a service to an interface.
type ApplyServiceOpts struct {
	IPAddress string            // IP address for routed/IRB services (e.g., "10.1.1.1/30"); for a local irb, the SVI gateway the composite authors
	VLAN      int               // VLAN ID for local types (irb, bridged) — overlay types use macvpnDef.VlanID
	PeerAS    int               // BGP peer AS number (for services with routing.peer_as="request")
	Params    map[string]string // topology params (peer_as, route_reflector_client, next_hop_self)
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

// InterfaceStatus is the composed live operational picture of one interface —
// link state, counters, rates, resolved neighbors, LLDP far end, optics —
// read across STATE_DB, APPL_DB, and COUNTERS_DB in one call. Pure
// observation (§4): values are reported as the daemons wrote them.
type InterfaceStatus struct {
	Name        string             `json:"name"`
	AdminStatus string             `json:"admin_status"`
	OperStatus  string             `json:"oper_status"`
	Speed       string             `json:"speed,omitempty"`
	MTU         string             `json:"mtu,omitempty"`
	FEC         string             `json:"fec,omitempty"`
	HostTxReady string             `json:"host_tx_ready,omitempty"`
	Counters    *InterfaceCounters `json:"counters,omitempty"`
	Rates       *InterfaceRates    `json:"rates,omitempty"`
	Neighbors   []ARPNeighbor      `json:"neighbors"`
	LLDPPeer    *LLDPPeer          `json:"lldp_peer,omitempty"`
	Optics      *OpticsInfo        `json:"optics,omitempty"`
	Members     []MemberStatus     `json:"members,omitempty"`
}

// MemberStatus is one constituent member port of a composite interface — a
// PortChannel member or an SVI's VLAN member — with its link state. Present on a
// PortChannel or VlanN status; omitted on a physical port (it has no members).
type MemberStatus struct {
	Name        string `json:"name"`
	AdminStatus string `json:"admin_status"`
	OperStatus  string `json:"oper_status"`
	Speed       string `json:"speed,omitempty"`
}

// InterfaceCounters holds the cumulative SAI port counters
// (COUNTERS_DB COUNTERS:<oid>).
type InterfaceCounters struct {
	RxOctets         uint64 `json:"rx_octets"`
	RxUnicastPackets uint64 `json:"rx_unicast_packets"`
	RxNonUnicastPkts uint64 `json:"rx_non_unicast_packets"`
	RxDiscards       uint64 `json:"rx_discards"`
	RxErrors         uint64 `json:"rx_errors"`
	TxOctets         uint64 `json:"tx_octets"`
	TxUnicastPackets uint64 `json:"tx_unicast_packets"`
	TxNonUnicastPkts uint64 `json:"tx_non_unicast_packets"`
	TxDiscards       uint64 `json:"tx_discards"`
	TxErrors         uint64 `json:"tx_errors"`
}

// InterfaceRates holds the SONiC-computed port rates (COUNTERS_DB
// RATES:<oid>) — no poll-twice-and-subtract needed.
type InterfaceRates struct {
	RxBps      float64 `json:"rx_bps"`
	RxPps      float64 `json:"rx_pps"`
	TxBps      float64 `json:"tx_bps"`
	TxPps      float64 `json:"tx_pps"`
	FecPreBer  float64 `json:"fec_pre_ber"`
	FecPostBer float64 `json:"fec_post_ber"`
}

// ARPNeighbor is one RESOLVED L3 adjacency on the interface (APPL_DB
// NEIGH_TABLE). The kernel does not publish INCOMPLETE entries to APPL_DB —
// an expected-but-absent neighbor IS the unresolved-ARP signal. The wire
// field is `address`, not `neighbor_ip`: an observed adjacency address, not
// a BGP peer identity (see api.md "Wire field-name conventions").
type ARPNeighbor struct {
	Address string `json:"address"`
	MAC     string `json:"mac"`
	Family  string `json:"family"`
}

// LLDPPeer is the interface's far end as LLDP reported it (APPL_DB
// LLDP_ENTRY_TABLE).
type LLDPPeer struct {
	ChassisID       string `json:"chassis_id"`
	PortID          string `json:"port_id"`
	PortDescription string `json:"port_description,omitempty"`
	SystemName      string `json:"system_name,omitempty"`
	SystemDesc      string `json:"system_description,omitempty"`
}

// OpticsInfo passes through the interface's transceiver tables (STATE_DB
// TRANSCEIVER_INFO / _DOM_SENSOR / _STATUS) as written — populated on
// physical hardware, absent on -vs platforms (pmon has no sensors to read).
// Raw maps rather than curated fields: the DOM schema varies by module type
// and the observation surface reports what exists.
type OpticsInfo struct {
	Present bool              `json:"present"`
	Info    map[string]string `json:"info,omitempty"`
	DOM     map[string]string `json:"dom,omitempty"`
	Status  map[string]string `json:"status,omitempty"`
}

// BGPNeighborEntry is a BGP neighbor from CONFIG_DB for VRF show.
// The wire name follows the intent-param vocabulary (neighbor_ip) — the
// registry manifest is the canonical wire vocabulary; see api.md
// "Wire field-name conventions".
type BGPNeighborEntry struct {
	Address     string `json:"neighbor_ip"`
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
	Address   string `json:"neighbor_ip"`
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
	Status      string              `json:"status"` // "pass", "warn", "fail"
	ConfigCheck *ConfigDriftResult  `json:"config_check,omitempty"`
	OperChecks  []HealthCheckResult `json:"oper_checks,omitempty"`
}

// ConfigDriftResult reports config drift found during health check.
type ConfigDriftResult struct {
	DriftCount int          `json:"drift_count"`
	Entries    []DriftEntry `json:"entries,omitempty"`
}

// HealthCheckResult represents the result of a single operational health check.
type HealthCheckResult struct {
	Check   string `json:"check"`   // Check name (e.g., "bgp", "interface-oper")
	Status  string `json:"status"`  // "pass", "warn", "fail"
	Message string `json:"message"` // Human-readable message
}

// ============================================================================
// Spec Detail Types (API view of spec objects)
// ============================================================================

// SpecInstance locates one spec definition within newtron's hierarchical spec
// store: its kind, its (canonical) name, and the scope + instance it is defined
// at. It is the flat, cross-scope inventory entry a schema-driven UI lists and
// filters by scope/scope_instance — the boundary projection of the
// network → zone → node hierarchy (definition is network-scoped, execution
// node-scoped; DESIGN_PRINCIPLES_NEWTRON §7). Scope is one of "network", "zone",
// "node"; ScopeInstance is the zone or node name, empty for the network scope.
type SpecInstance struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Scope         string `json:"scope"`
	ScopeInstance string `json:"scope_instance"`
}

// ServiceDetail is the API view of a service definition.
type ServiceDetail struct {
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	ServiceType   string         `json:"service_type"`
	IPVPN         string         `json:"ipvpn,omitempty"`
	MACVPN        string         `json:"macvpn,omitempty"`
	VRFType       string         `json:"vrf_type,omitempty"`
	QoSPolicy     string         `json:"qos_policy,omitempty"`
	IngressFilter string         `json:"ingress_filter,omitempty"`
	EgressFilter  string         `json:"egress_filter,omitempty"`
	Routing       *RoutingDetail `json:"routing,omitempty"`
}

// RoutingDetail is the API view of a routed service's BGP/static routing
// configuration. It is the read mirror of CreateServiceRouting — every field
// accepted on create-service / update-service is returned on the service read,
// so routing config is no longer write-only (ai-instructions §24).
type RoutingDetail struct {
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

// IPVPNDetail is the API view of an IP-VPN definition. Name is the
// operator-facing identifier. There is no single VRF name: an IP-VPN is a
// virtual network, and any number of VRFs (named after their services, or by an
// operator via `vrf bind-ipvpn`) join it as members carrying its shared L3VNI.
type IPVPNDetail struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
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

// ============================================================================
// NodeSpec and Zone Detail Types
// ============================================================================

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

// SetupDeviceOpts holds configuration for the consolidated SetupDevice operation.
type SetupDeviceOpts struct {
	Fields   map[string]string   `json:"fields,omitempty"`    // device metadata fields
	SourceIP string              `json:"source_ip,omitempty"` // VTEP source IP (empty = skip)
	RR       *RouteReflectorOpts `json:"route_reflector,omitempty"`
}

// ZoneDetail is the API view of a zone definition.
type ZoneDetail struct {
	Name string `json:"name"`
}

// AuthorizationDetail is the API view of the network's authorization
// table — the user_groups, permissions, and super_users an operator
// authors in network.json and that newtron's authorization checker
// consumes at every mutation (auth-design.md §L3). The three fields
// mirror NetworkSpecFile.{UserGroups, Permissions, SuperUsers}
// directly (DPN §46 — wire shape mirrors canonical types). One
// endpoint returns all three because they form one cohesive object
// authored together, applied together on
// --enforce-authorization + reload, and consumed together by the
// checker (DPN §27).
//
// Permissions values are emitted in whichever wire form their
// custom MarshalJSON chooses: the legacy ["group", ...] shorthand
// when every grant has an empty `where` clause, the typed
// [{groups, where}] form when any grant carries a scope. The wire
// shape matches what an operator would see in network.json for the
// same data, so a "who has what" inspector mounted on this
// endpoint reads byte-for-byte like the spec file.
type AuthorizationDetail struct {
	UserGroups  map[string][]string              `json:"user_groups"`
	Permissions map[string]spec.PermissionGrants `json:"permissions"`
	SuperUsers  []string                         `json:"super_users"`
}

// CreateNodeSpecRequest is the request for creating a node spec.
type CreateNodeSpecRequest struct {
	Name        string                   `json:"name"`
	MgmtIP      string                   `json:"mgmt_ip"`
	LoopbackIP  string                   `json:"loopback_ip"`
	Zone        string                   `json:"zone"`
	Platform    string                   `json:"platform,omitempty"`
	MAC         string                   `json:"mac,omitempty"`
	UnderlayASN int                      `json:"underlay_asn,omitempty"`
	EVPN        *CreateEVPNConfigRequest `json:"evpn,omitempty"`
	// No ssh_user/ssh_pass: the device SSH login is authored uniformly at any
	// scope via set-ssh-credentials (network/zone/node), not on the node body —
	// the flat-hierarchy pattern's single authoring path (§27). A node inherits
	// the network login unless a scoped override is set.
}

// CreateEVPNConfigRequest defines EVPN peering for nodeSpec creation.
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

// ScopeSelector carries the optional scope discriminators on a spec write —
// the "flat at the boundary, hierarchical underneath" surface (P2). Embedded in
// the spec write requests. An absent/empty Scope means network scope, which
// preserves pre-scope behavior exactly (so existing callers are unaffected).
// ScopeInstance is the zone or node name; it is required when Scope is "zone"
// or "node" and ignored for "network".
//
// Writes follow the network-floor invariant (DESIGN_PRINCIPLES_NEWTRON §7): a
// zone/node override may be created only if a network-scope definition of the
// same name already exists. The server enforces this and returns a typed 400
// when it is violated, so the UI needs no bespoke rule — it offers "override"
// only on resources the /spec-instances inventory shows at network scope.
type ScopeSelector struct {
	Scope         string `json:"scope,omitempty" label:"Scope" tooltip:"Where this definition lives: network (default), or a zone/node override" enum:"network,zone,node"`
	ScopeInstance string `json:"scope_instance,omitempty" label:"Scope Instance" tooltip:"Zone or node name for a scoped override; empty for network scope"`
}

// SetSSHCredentialsRequest sets the device SSH login (ssh_user / ssh_pass) at a
// scope — the scalar analog of the map-overridable spec writes. Same
// ScopeSelector surface as create-service / create-ipvpn ("flat at the boundary,
// hierarchical underneath", §7), but the login is one value per scope rather than
// a named collection, so there is no name field. Either field may be empty,
// meaning "inherit from the next scope up"; ssh_pass may be a ${secret:KEY}
// reference (the masked input on the UI, secret:"true").
//
// Network-floor invariant (§7) applies, as it does to every overridable: a
// zone/node login override requires a network-scope login, and the network base
// cannot be emptied while an override sits below it (clear bottom-up).
type SetSSHCredentialsRequest struct {
	ScopeSelector
	SSHUser string `json:"ssh_user,omitempty" label:"SSH User" tooltip:"Username for the SSH tunnel at this scope; empty inherits from the next scope up (node > zone > network), then the platform default, then \"admin\"."`
	SSHPass string `json:"ssh_pass,omitempty" label:"SSH Password" tooltip:"Password, or a ${secret:KEY} reference, for the SSH tunnel at this scope; empty inherits from the next scope up." secret:"true"`
}

// SSHCredentialsView is the read of the login AUTHORED at one scope (GET
// ssh-credentials), the mirror of SetSSHCredentialsRequest (§24). ssh_user is
// returned verbatim. ssh_pass is masked: a ${secret:KEY} reference is returned
// as-is (a pointer, not a secret — so the UI can show which key is referenced), a
// plaintext value becomes "***redacted***" (a value is set but never echoed), and
// an empty value stays empty (nothing authored at this scope). Scope /
// ScopeInstance echo the request as read-only provenance.
type SSHCredentialsView struct {
	Scope         string `json:"scope"`
	ScopeInstance string `json:"scope_instance"`
	SSHUser       string `json:"ssh_user,omitempty"`
	SSHPass       string `json:"ssh_pass,omitempty"`
}

// CreateServiceRequest is the request for creating a service definition.
type CreateServiceRequest struct {
	ScopeSelector
	Name          string                `json:"name"`
	ServiceType   string                `json:"service_type"`
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
// Name is a normal, canonicalized spec name. An IP-VPN has no VRF name of its
// own — VRFs (named after their services, or by an operator via `vrf
// bind-ipvpn`) join the VPN as members carrying its shared L3VNI.
type CreateIPVPNRequest struct {
	ScopeSelector
	Name         string   `json:"name"`
	L3VNI        int      `json:"l3vni"`
	RouteTargets []string `json:"route_targets,omitempty"`
	Description  string   `json:"description,omitempty"`
}

// CreateMACVPNRequest is the request for creating a MAC-VPN definition.
type CreateMACVPNRequest struct {
	ScopeSelector
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
	ScopeSelector
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AddQoSQueueRequest is the request for adding a queue to a QoS policy.
type AddQoSQueueRequest struct {
	ScopeSelector
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
	ScopeSelector
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// AddFilterRuleRequest is the request for adding a rule to a filter.
type AddFilterRuleRequest struct {
	ScopeSelector
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

// UpdateQoSQueueRequest is the request for updating an existing queue
// in a QoS policy. Mirrors UpdateFilterRuleRequest. QueueID identifies
// the existing queue (matches RemoveQoSQueue's parameter); NewQueueID
// is optional — when non-nil the queue rotates to that slot. Issue #211.
type UpdateQoSQueueRequest struct {
	ScopeSelector
	Policy     string `json:"policy"`
	QueueID    int    `json:"queue_id"`
	NewQueueID *int   `json:"new_queue_id,omitempty"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Weight     int    `json:"weight,omitempty"`
	DSCP       []int  `json:"dscp,omitempty"`
	ECN        bool   `json:"ecn,omitempty"`
}

// UpdateRoutePolicyRuleRequest is the request for updating an existing
// rule in a route policy. Mirrors UpdateFilterRuleRequest. Issue #210.
type UpdateRoutePolicyRuleRequest struct {
	ScopeSelector
	Policy      string              `json:"policy"`
	Sequence    int                 `json:"seq"`
	NewSequence *int                `json:"new_seq,omitempty"`
	Action      string              `json:"action"`
	PrefixList  string              `json:"prefix_list,omitempty"`
	Community   string              `json:"community,omitempty"`
	Set         *RoutePolicySetSpec `json:"set,omitempty"`
}

// UpdateFilterRuleRequest is the request for updating an existing rule in
// a filter. Sequence identifies the existing rule (matches RemoveFilterRule
// semantics). NewSequence is optional — when present and non-zero, the
// rule's sequence rotates to that value (renumber); when absent, the
// rule keeps its current sequence. Remaining fields replace the rule's
// current values. Issue #209.
type UpdateFilterRuleRequest struct {
	ScopeSelector
	Filter        string `json:"filter"`
	Sequence      int    `json:"seq"`
	NewSequence   *int   `json:"new_seq,omitempty"`
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
	ScopeSelector
	Name     string   `json:"name"`
	Prefixes []string `json:"prefixes,omitempty"`
}

// AddPrefixListEntryRequest is the request for adding an entry to a prefix list.
type AddPrefixListEntryRequest struct {
	ScopeSelector
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
	ScopeSelector
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CreatePlatformRequest is the request for creating (or, via the
// shared shape, updating) a platform definition. Embeds spec.PlatformSpec
// so the wire shape mirrors the canonical type byte-for-byte (DPN §46) —
// an operator can copy a `platforms.json` entry directly into the
// request body and the loader will accept it unchanged.
//
// The embedded PlatformSpec fields appear at the same JSON level as
// Name. The `credentials` field's ${secret:KEY} references are a load-time
// mechanism and are not re-resolved on Save (#173 — see SavePlatforms doc).
type CreatePlatformRequest struct {
	Name string `json:"name"`
	spec.PlatformSpec
}

// AddRoutePolicyRuleRequest is the request for adding a rule to a route policy.
type AddRoutePolicyRuleRequest struct {
	ScopeSelector
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

// IntentTreeNode represents a node in the intent DAG tree display.
// Used by the intent tree CLI command and API endpoint (§12).
type IntentTreeNode struct {
	Resource  string            `json:"resource"`
	Operation string            `json:"operation"`
	Params    map[string]string `json:"params,omitempty"`
	Children  []IntentTreeNode  `json:"children,omitempty"`
	Leaf      bool              `json:"leaf,omitempty"` // multi-parent: rendered as leaf under this parent
}

// ============================================================================
// Audit Types
// ============================================================================

// AuditFilter defines criteria for querying audit events.
type AuditFilter struct {
	// Network scopes results to one network's events. The per-network
	// read handlers set it from the request path's {netID} so a caller
	// authorized for one network cannot read another's audit through it.
	// Empty matches every network (the CLI explicit-path forensic case).
	Network     string
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
	// Order selects result ordering ("desc"/"" = newest first, the default;
	// "asc" = chronological), applied before Offset/Limit. See audit.Filter.
	Order string
}

// AuditEvent represents an auditable configuration change event.
type AuditEvent struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	User      string `json:"user"`
	// VerificationSource names how User was established: a verified source
	// (pam, session_key, service_cert_cn, unix_peer_creds), the unverified
	// self_attested_header, or anonymous — the request carried no identity and
	// the server accepted it in permissive mode. A reviewer reads User together
	// with this: an empty User with "anonymous" is an expected permissive-mode
	// record, not a missing-data defect.
	VerificationSource string        `json:"verification_source,omitempty"`
	Network            string        `json:"network,omitempty"`
	Device             string        `json:"device"`
	Operation          string        `json:"operation"`
	Service            string        `json:"service,omitempty"`
	Interface          string        `json:"interface,omitempty"`
	Changes            []AuditChange `json:"changes"`
	// RequestBody is the redacted JSON the caller submitted. Populated only by
	// the per-event detail endpoint (GET …/audit/events/{id}); the paged list
	// leaves it empty so the list stays lean. omitempty keeps it off list rows.
	RequestBody json.RawMessage `json:"request_body,omitempty"`
	Success     bool            `json:"success"`
	Error       string          `json:"error,omitempty"`
	ExecuteMode bool            `json:"execute_mode"`
	DryRun      bool            `json:"dry_run"`
	Duration    string          `json:"duration"`
	ClientIP    string          `json:"client_ip,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
}

// AuditChange is a single CONFIG_DB change within an audit event. Fields is the
// after-state (the values written; empty on a delete); From is the before-state
// (the values overwritten or deleted; empty on an add) — together they make the
// change reversible without re-reading the device (issue #236).
type AuditChange struct {
	Table  string            `json:"table"`
	Key    string            `json:"key"`
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields,omitempty"`
	From   map[string]string `json:"from,omitempty"`
}

// AuditEventPage is the wire shape returned by GET /audit/events
// (issue #196). Pairs the events slice with paging metadata so a
// browsing UI knows whether more entries are available.
//
// Per §46: the events field carries AuditEvent values directly —
// the same shape the audit middleware writes. Total reflects the
// full filtered-but-not-paginated count so the client can render
// "N of M entries"; NextOffset is non-nil when more entries remain
// past the current page, nil when the page exhausted the filter.
type AuditEventPage struct {
	Events     []AuditEvent `json:"events"`
	Total      int          `json:"total"`
	NextOffset *int         `json:"next_offset,omitempty"`
}

// AuditIntegrityResult is the wire shape returned by GET /audit/integrity
// (issue #196 / auth-design.md L6). Mirrors audit.VerifyResult with
// JSON field names. The result reflects an end-to-end walk of the
// hash chain at request time — operators run it periodically and on
// suspected tamper.
//
// Field meanings:
//
//   - ChainHeadHash: the running chain head after walking every
//     entry. Stable across calls when the log is unmodified; an
//     operator can record this and re-check later as a cheap tripwire.
//   - EntryCount: the number of integrity-enabled entries scanned.
//     Pre-L6 entries (empty ID) are tolerated and counted as scanned
//     but not chained.
//   - BreakAt: the line number of the first entry whose chain link
//     didn't verify, or 0 if the chain is clean end to end.
//   - BreakReason: "prev_hash mismatch" or "id mismatch" describing
//     the failure mode at BreakAt, or empty for a clean chain.
//   - VerifiedAt: the server-side timestamp of the verification.
//     Callers can cache the result client-side keyed on this value.
type AuditIntegrityResult struct {
	ChainHeadHash string `json:"chain_head_hash"`
	EntryCount    int    `json:"entry_count"`
	// BreakAt and BreakReason are NOT omitempty: a clean chain has
	// BreakAt=0 and BreakReason="", and operators check those values
	// to decide cleanliness. Omitting them would make ".break_at == 0"
	// fail because the field would be missing rather than 0.
	BreakAt     int    `json:"break_at"`
	BreakReason string `json:"break_reason"`
	VerifiedAt  string `json:"verified_at"`
}

// ============================================================================
// Settings Types
// ============================================================================

// UserSettings holds persistent user preferences.
type UserSettings struct {
	DefaultNetwork string `json:"default_network,omitempty"`
	Dir            string `json:"dir,omitempty"`
	DefaultSuite   string `json:"default_suite,omitempty"`
	NetworksDir    string `json:"networks_dir,omitempty"`
	ServerURL      string `json:"server_url,omitempty"`
	NetworkID      string `json:"network_id,omitempty"`
}

// DefaultDir is the default specification directory.
const DefaultDir = "/etc/newtron"

// GetDir returns the network directory with a fallback default.
func (us *UserSettings) GetDir() string {
	if us.Dir != "" {
		return us.Dir
	}
	return DefaultDir
}

// DefaultNetworkID is the default network identifier.
const DefaultNetworkID = "default"

// GetServerURL returns the configured server URL, or the canonical default
// (httputil.DefaultServerURL — the single owner shared with every other
// client) when unset.
func (us *UserSettings) GetServerURL() string {
	if us.ServerURL != "" {
		return us.ServerURL
	}
	return httputil.DefaultServerURL
}

// GetNetworkID returns the network ID with a fallback default.
func (us *UserSettings) GetNetworkID() string {
	if us.NetworkID != "" {
		return us.NetworkID
	}
	return DefaultNetworkID
}

// ============================================================================
// Intent Operation Result Types
// ============================================================================

// ReconcileOpts controls the Reconcile delivery mechanism.
type ReconcileOpts struct {
	Mode string `json:"mode"` // "full" or "delta"
}

// ReconcileResult reports the outcome of delivering the projection to a device.
type ReconcileResult struct {
	Mode     string `json:"mode"`               // "full" or "delta"
	Applied  int    `json:"applied"`            // total entries touched
	Missing  int    `json:"missing,omitempty"`  // entries added (delta only)
	Extra    int    `json:"extra,omitempty"`    // entries removed (delta only)
	Modified int    `json:"modified,omitempty"` // entries corrected (delta only)
	Message  string `json:"message,omitempty"`
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

// TopologySnapshot is the device's actuated intents projected as topology steps.
// Returned by Snapshot() — the export direction: device reality → topology format.
type TopologySnapshot struct {
	Steps []TopologyStep `json:"steps,omitempty"`
}

// TopologyStep is a single provisioning operation in topology format.
//
// SpecKind and SpecName are server-derived provenance: when the step is the
// instantiation of a named network spec (a service applied, an IP-VPN/MAC-VPN
// bound, a QoS policy bound), they name the source spec so a client can map
// intent → spec without re-implementing newtron's per-operation derivation
// (see DeriveSpecRef). They are output-only — populated by Tree() at serve
// time, empty for primitives, and ignored on input. They are NOT persisted to
// topology.json (that uses spec.TopologyStep); they re-derive on every serve,
// so they can never go stale.
type TopologyStep struct {
	URL      string         `json:"url"`
	Params   map[string]any `json:"params,omitempty"`
	SpecKind string         `json:"spec_kind,omitempty"`
	SpecName string         `json:"spec_name,omitempty"`
}

// TopologyView is the served shape of GET /topology. It mirrors the on-disk
// spec.TopologySpecFile JSON exactly, except its steps are the public
// TopologyStep — carrying server-derived spec_kind/spec_name (DeriveSpecRef) so
// a client gets spec provenance for a whole network in one call, before any lab
// is deployed (it's a spec-file read). The derived fields are output-only and
// re-computed each serve; the on-disk spec.TopologySpecFile is untouched.
// Links and NewtLab pass through as the spec types — same as the prior raw
// served shape; only steps are enriched.
type TopologyView struct {
	Version     string                       `json:"version"`
	Platform    string                       `json:"platform,omitempty"`
	Description string                       `json:"description,omitempty"`
	Nodes       map[string]*TopologyNodeView `json:"nodes"`
	Links       []*spec.TopologyLink         `json:"links,omitempty"`
	NewtLab     *spec.NewtLabConfig          `json:"newtlab,omitempty"`
}

// TopologyNodeView mirrors spec.TopologyNode with provenance-bearing steps.
type TopologyNodeView struct {
	Steps []TopologyStep         `json:"steps,omitempty"`
	Ports map[string]*PortConfig `json:"ports,omitempty"`
}

// PortConfig is the public view of a topology device's per-port config — the
// domain-vocabulary mirror of spec.PortConfig, so TopologyNodeView exposes no
// internal spec type at the API boundary (§33). The wire shape is identical.
type PortConfig struct {
	AdminStatus string `json:"admin_status,omitempty"`
	MTU         int    `json:"mtu,omitempty"`
	Speed       string `json:"speed,omitempty"`
	Description string `json:"description,omitempty"`
}

// toPortConfigView mirrors a spec.PortConfig into the public PortConfig at the
// API boundary; nil maps to nil so a null port entry round-trips faithfully.
func toPortConfigView(pc *spec.PortConfig) *PortConfig {
	if pc == nil {
		return nil
	}
	return &PortConfig{
		AdminStatus: pc.AdminStatus,
		MTU:         pc.MTU,
		Speed:       pc.Speed,
		Description: pc.Description,
	}
}

// ============================================================================
// Host Types
// ============================================================================

// HostConnection contains SSH connection parameters for a virtual host device.
type HostConnection struct {
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

// IRBConfigureRequest is the request body for configuring an IRB.
type IRBConfigureRequest struct {
	VlanID     int    `json:"vlan_id"`
	VRF        string `json:"vrf,omitempty"`
	IPAddress  string `json:"ip_address,omitempty"`
	AnycastMAC string `json:"anycast_mac,omitempty"`
}

// Config converts the wire request to the domain config the Node API takes
// (§33 boundary translation) — see ACLRuleAddRequest.Config for why the
// conversion has exactly one site.
func (r IRBConfigureRequest) Config() IRBConfig {
	return IRBConfig{
		VRF:        r.VRF,
		IPAddress:  r.IPAddress,
		AnycastMAC: r.AnycastMAC,
	}
}

// ACLCreateRequest is the request body for creating an ACL table.
type ACLCreateRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Stage       string `json:"stage"`
	Ports       string `json:"ports,omitempty"`
	Description string `json:"description,omitempty"`
}

// Config converts the wire request to the domain config — see
// ACLRuleAddRequest.Config.
func (r ACLCreateRequest) Config() ACLConfig {
	return ACLConfig{
		Type:        r.Type,
		Stage:       r.Stage,
		Ports:       r.Ports,
		Description: r.Description,
	}
}

// ACLRuleAddRequest is the request body for adding a rule to an ACL table.
// It is the single owner of the wire shape — the client sends it verbatim
// and the handler decodes it verbatim (§25: no per-site field copies).
type ACLRuleAddRequest struct {
	ACL      string `json:"acl"`
	RuleName string `json:"rule_name"`
	Priority int    `json:"priority"`
	Action   string `json:"action"`
	SrcIP    string `json:"src_ip,omitempty"`
	DstIP    string `json:"dst_ip,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	SrcPort  string `json:"src_port,omitempty"`
	DstPort  string `json:"dst_port,omitempty"`
}

// Config converts the wire request to the domain config the Node API takes
// (§33 boundary translation). The single conversion site for this family —
// a handler that copies fields by hand re-creates the silent-drop boundary
// RCA-049 documented. Enforced by TestHandlersUseConfigConverters.
func (r ACLRuleAddRequest) Config() ACLRuleConfig {
	return ACLRuleConfig{
		Priority: r.Priority,
		Action:   r.Action,
		SrcIP:    r.SrcIP,
		DstIP:    r.DstIP,
		Protocol: r.Protocol,
		SrcPort:  r.SrcPort,
		DstPort:  r.DstPort,
	}
}

// ACLRuleUpdateRequest is the request body for atomically updating an ACL
// rule's fields under the per-device intent lock. The composite key
// (table + rule_name) is the row's identity (§47) and is not mutable
// through this verb — rename via remove-acl-rule + add-acl-rule. #227.
type ACLRuleUpdateRequest struct {
	ACL      string `json:"acl"`
	RuleName string `json:"rule_name"`
	Priority int    `json:"priority"`
	Action   string `json:"action"`
	SrcIP    string `json:"src_ip,omitempty"`
	DstIP    string `json:"dst_ip,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	SrcPort  string `json:"src_port,omitempty"`
	DstPort  string `json:"dst_port,omitempty"`
}

// Config converts the wire request to the domain config — see
// ACLRuleAddRequest.Config.
func (r ACLRuleUpdateRequest) Config() ACLRuleConfig {
	return ACLRuleConfig{
		Priority: r.Priority,
		Action:   r.Action,
		SrcIP:    r.SrcIP,
		DstIP:    r.DstIP,
		Protocol: r.Protocol,
		SrcPort:  r.SrcPort,
		DstPort:  r.DstPort,
	}
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
// Config converts the wire request to the domain config — see
// ACLRuleAddRequest.Config.
func (r PortChannelCreateRequest) Config() PortChannelConfig {
	return PortChannelConfig{
		Members:  r.Members,
		MinLinks: r.MinLinks,
		FastRate: r.FastRate,
		Fallback: r.Fallback,
		MTU:      r.MTU,
	}
}
