package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// APIResponse is the standard envelope for all API responses.
type APIResponse struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// ============================================================================
// HTTP Request Types — Network
// ============================================================================

// RegisterNetworkRequest is the body for POST /network.
type RegisterNetworkRequest struct {
	ID      string `json:"id"`
	SpecDir string `json:"spec_dir"`
}

// NetworkInfo is returned when listing or showing a registered network.
type NetworkInfo struct {
	ID          string   `json:"id"`
	SpecDir     string   `json:"spec_dir"`
	HasTopology bool     `json:"has_topology"`
	Nodes       []string `json:"nodes"`
}

// ============================================================================
// HTTP Request Types — Node Operations
// ============================================================================

// ExecuteRequest batches multiple operations in a single connect cycle.
type ExecuteRequest struct {
	Operations []Operation `json:"operations"`
	Execute    bool        `json:"execute"`
	NoSave     bool        `json:"no_save"`
}

// Operation is a single action within an ExecuteRequest.
type Operation struct {
	Action    string         `json:"action"`
	Interface string         `json:"interface,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

// SSHCommandRequest is the body for POST .../ssh-command.
type SSHCommandRequest struct {
	Command string `json:"command"`
}

// SSHCommandResponse wraps the output of an SSH command.
type SSHCommandResponse struct {
	Output string `json:"output"`
}

// SetupEVPNRequest is the body for POST .../setup-evpn.
type SetupEVPNRequest struct {
	SourceIP string `json:"source_ip"`
}

// CompositeHandleRequest references a stored composite by UUID.
type CompositeHandleRequest struct {
	Handle string `json:"handle"`
	Mode   string `json:"mode,omitempty"` // "overwrite" or "merge"
}

// CompositeHandleResponse is returned by GenerateComposite.
type CompositeHandleResponse struct {
	Handle     string         `json:"handle"`
	DeviceName string         `json:"device_name"`
	EntryCount int            `json:"entry_count"`
	Tables     map[string]int `json:"tables"`
}

// CleanupRequest is the body for POST .../cleanup.
type CleanupRequest struct {
	Type string `json:"type,omitempty"` // "acls", "vrfs", "vnis", or "" for all
}

// ConfigReloadRequest is the body for POST .../config-reload (currently empty).
type ConfigReloadRequest struct{}

// SaveConfigRequest is the body for POST .../save-config (currently empty).
type SaveConfigRequest struct{}

// ============================================================================
// HTTP Request Types — Interface Operations
// ============================================================================

// ApplyServiceRequest is the body for POST .../apply-service.
type ApplyServiceRequest struct {
	Service   string            `json:"service"`
	IPAddress string            `json:"ip_address,omitempty"`
	VLAN      int               `json:"vlan,omitempty"`
	PeerAS    int               `json:"peer_as,omitempty"`
	Params    map[string]string `json:"params,omitempty"`
}

// SetIPRequest is the body for POST .../set-ip.
type SetIPRequest struct {
	IP string `json:"ip"`
}

// RemoveIPRequest is the body for POST .../remove-ip.
type RemoveIPRequest struct {
	IP string `json:"ip"`
}

// SetVRFRequest is the body for POST .../set-vrf.
type SetVRFRequest struct {
	VRF string `json:"vrf"`
}

// BindACLRequest is the body for POST .../bind-acl.
type BindACLRequest struct {
	ACL       string `json:"acl"`
	Direction string `json:"direction"`
}

// UnbindACLRequest is the body for POST .../unbind-acl.
type UnbindACLRequest struct {
	ACL string `json:"acl"`
}

// BindMACVPNRequest is the body for POST .../bind-macvpn.
type BindMACVPNRequest struct {
	MACVPN string `json:"macvpn"`
}

// InterfaceSetRequest is the body for POST .../set.
type InterfaceSetRequest struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// ApplyQoSRequest is the body for POST .../apply-qos.
type ApplyQoSRequest struct {
	Policy string `json:"policy"`
}

// RouteRequest identifies a route lookup.
type RouteRequest struct {
	VRF    string `json:"vrf"`
	Prefix string `json:"prefix"`
}

// ============================================================================
// HTTP Request Types — Node write operations that need JSON bodies
// ============================================================================

// VLANCreateRequest is the body for POST .../vlan.
type VLANCreateRequest struct {
	ID          int    `json:"id"`
	Description string `json:"description,omitempty"`
}

// SVIConfigureRequest is the body for POST .../svi.
type SVIConfigureRequest struct {
	VlanID     int    `json:"vlan_id"`
	VRF        string `json:"vrf,omitempty"`
	IPAddress  string `json:"ip_address,omitempty"`
	AnycastMAC string `json:"anycast_mac,omitempty"`
}

// VRFCreateRequest is the body for POST .../vrf.
type VRFCreateRequest struct {
	Name string `json:"name"`
}

// ACLCreateRequest is the body for POST .../acl.
type ACLCreateRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Stage       string `json:"stage"`
	Ports       string `json:"ports,omitempty"`
	Description string `json:"description,omitempty"`
}

// ACLRuleAddRequest is the body for POST .../acl/{name}/rule.
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

// PortChannelCreateRequest is the body for POST .../portchannel.
type PortChannelCreateRequest struct {
	Name     string   `json:"name"`
	Members  []string `json:"members,omitempty"`
	MinLinks int      `json:"min_links,omitempty"`
	FastRate bool     `json:"fast_rate,omitempty"`
	Fallback bool     `json:"fallback,omitempty"`
	MTU      int      `json:"mtu,omitempty"`
}

// PortChannelMemberRequest is the body for POST .../portchannel/{name}/member.
type PortChannelMemberRequest struct {
	Interface string `json:"interface"`
}

// ============================================================================
// Error mapping
// ============================================================================

// notRegisteredError is returned when a network ID is not registered.
type notRegisteredError struct {
	id string
}

func (e *notRegisteredError) Error() string {
	return "network '" + e.id + "' not registered"
}

// httpStatusFromError maps Go error types to HTTP status codes.
func httpStatusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}

	var notReg *notRegisteredError
	if errors.As(err, &notReg) {
		return http.StatusNotFound
	}

	var notFound *newtron.NotFoundError
	if errors.As(err, &notFound) {
		return http.StatusNotFound
	}

	var validation *newtron.ValidationError
	if errors.As(err, &validation) {
		return http.StatusBadRequest
	}

	var verificationFailed *newtron.VerificationFailedError
	if errors.As(err, &verificationFailed) {
		return http.StatusConflict
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}

	return http.StatusInternalServerError
}
