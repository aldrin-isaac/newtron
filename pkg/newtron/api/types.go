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
	Topology    string   `json:"topology,omitempty"` // topology name derived from specDir
	Nodes       []string `json:"nodes"`
}

// ============================================================================
// HTTP Request Types — Node Operations
// ============================================================================

// SSHCommandRequest is the body for POST .../ssh-command.
type SSHCommandRequest struct {
	Command string `json:"command"`
}

// SSHCommandResponse wraps the output of an SSH command.
type SSHCommandResponse struct {
	Output string `json:"output"`
}

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

// BindACLRequest is the body for POST .../bind-acl.
type BindACLRequest struct {
	ACL       string `json:"acl"`
	Direction string `json:"direction"`
}

// UnbindACLRequest is the body for POST .../unbind-acl.
type UnbindACLRequest struct {
	ACL string `json:"acl"`
}

// InterfaceSetRequest is the body for POST .../set-property.
type InterfaceSetRequest struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// InterfaceClearRequest is the body for POST .../clear-property.
type InterfaceClearRequest struct {
	Property string `json:"property"`
}

// ConfigureInterfaceRequest is the body for POST .../configure-interface.
// Routed mode (VRF+IP) and bridged mode (VLAN) are mutually exclusive.
type ConfigureInterfaceRequest struct {
	VRF    string `json:"vrf,omitempty"`
	IP     string `json:"ip,omitempty"`
	VLAN   int    `json:"vlan_id,omitempty"`
	Tagged bool   `json:"tagged,omitempty"`
}

// NodeBindMACVPNRequest is the body for POST .../bind-macvpn (node-level, maps VLAN to L2VNI).
type NodeBindMACVPNRequest struct {
	VlanID int    `json:"vlan_id"`
	MACVPN string `json:"macvpn"`
}

// NodeUnbindMACVPNRequest is the body for POST .../unbind-macvpn (node-level).
type NodeUnbindMACVPNRequest struct {
	VlanID int `json:"vlan_id"`
}

// ApplyQoSRequest is the body for POST .../apply-qos.
type ApplyQoSRequest struct {
	Policy string `json:"policy"`
}

// ============================================================================
// HTTP Request Types — Node write operations that need JSON bodies
// ============================================================================

// VLANCreateRequest is the body for POST .../create-vlan.
type VLANCreateRequest struct {
	ID          int    `json:"id"`
	Description string `json:"description,omitempty"`
}

// IRBConfigureRequest is the body for POST .../configure-irb.
type IRBConfigureRequest = newtron.IRBConfigureRequest

// VRFCreateRequest is the body for POST .../create-vrf.
type VRFCreateRequest struct {
	Name string `json:"name"`
}

// ACLCreateRequest is the body for POST .../create-acl.
type ACLCreateRequest = newtron.ACLCreateRequest

// ACLRuleAddRequest is the body for POST .../add-acl-rule.
type ACLRuleAddRequest = newtron.ACLRuleAddRequest

// PortChannelCreateRequest is the body for POST .../create-portchannel.
type PortChannelCreateRequest = newtron.PortChannelCreateRequest

// PortChannelMemberRequest is the body for POST .../add-portchannel-member.
type PortChannelMemberRequest struct {
	PortChannel string `json:"portchannel"`
	Interface   string `json:"interface"`
}

// ============================================================================
// HTTP Request Types — Missing Node Operations
// ============================================================================

// UnconfigureIRBRequest is the body for POST .../unconfigure-irb.
type UnconfigureIRBRequest struct {
	VlanID int `json:"vlan_id"`
}

// BindIPVPNRequest is the body for POST .../bind-ipvpn.
type BindIPVPNRequest struct {
	VRF   string `json:"vrf"`
	IPVPN string `json:"ipvpn"`
}

// StaticRouteRequest is the body for POST .../add-static-route.
type StaticRouteRequest struct {
	VRF     string `json:"vrf"`
	Prefix  string `json:"prefix"`
	NextHop string `json:"nexthop"`
	Metric  int    `json:"metric,omitempty"`
}

// RestartDaemonRequest is the body for POST .../restart-daemon.
type RestartDaemonRequest struct {
	Daemon string `json:"daemon"`
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

// alreadyRegisteredError is returned when a network ID is already registered.
type alreadyRegisteredError struct {
	id string
}

func (e *alreadyRegisteredError) Error() string {
	return "network '" + e.id + "' already registered"
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

	var alreadyReg *alreadyRegisteredError
	if errors.As(err, &alreadyReg) {
		return http.StatusConflict
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
