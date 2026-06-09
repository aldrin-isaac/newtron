package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)


// ============================================================================
// HTTP Request Types — Network
// ============================================================================

// RegisterNetworkRequest is the body for POST /network.
//
// When Scaffold is true, the server creates an empty spec layout at SpecDir
// (three zero-valued spec files plus an empty profiles/ subdirectory) before
// registering, and Description seeds topology.json. When Scaffold is false
// (the default), SpecDir must already contain valid specs — the same
// register-existing flow the endpoint has always had.
//
// Scaffold + register-existing as one wire operation avoids a two-call
// dance (scaffold-then-register) and keeps newtron the sole owner of the
// spec-directory layout (§27 Single Owner).
//
// SpecDir is required for the register-existing case (scaffold=false). For
// the scaffold case it is optional: when omitted with scaffold=true, the
// server derives the path from its --scaffold-root config as
// filepath.Join(scaffoldRoot, id), so UI clients don't have to know
// newtron's on-disk layout (§33; #122). The resolved spec_dir is always
// returned in the 201 response (NetworkInfo) so the caller can display
// "created at <path>" without re-fetching.
type RegisterNetworkRequest struct {
	ID          string `json:"id"`
	SpecDir     string `json:"spec_dir,omitempty"`
	Scaffold    bool   `json:"scaffold,omitempty"`
	Description string `json:"description,omitempty"`
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

// ProjectionDiffRequest is the body for POST .../intent/projection-diff.
// Operations are TopologyStep entries in the same shape /execute and
// /intent/save consume. The server applies them in-memory only, captures the
// resulting projection, and restores the Node's observable state before
// responding with ProjectionDiffResult (before / after / diff).
type ProjectionDiffRequest struct {
	Operations []spec.TopologyStep `json:"operations"`
}

// TopologyNodeCreateRequest is the body for POST .../topology/create-node.
// Name addresses the new entry; Device carries the typed TopologyDevice as
// stored in topology.json (profile is implicit via name; Ports + Steps may
// be empty for a bare declaration, or pre-populated for one-shot create).
type TopologyNodeCreateRequest struct {
	Name   string               `json:"name"`
	Device *spec.TopologyDevice `json:"device"`
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
// HTTP Response Types — Node Status (issue #75A)
// ============================================================================

// IntentSource enumerates the source the cached projection was built from.
// Values mirror the Mode enum in mode.go (§13: same concept = same name);
// IntentSourceUnloaded is the wire-only addition for the case where the
// actor has never been touched — a state with no in-memory representation
// but a real one for the operator.
type IntentSource string

const (
	IntentSourceIntent   IntentSource = "intent"   // matches ModeIntent (device-actuated)
	IntentSourceTopology IntentSource = "topology" // matches ModeTopology
	IntentSourceLoopback IntentSource = "loopback" // matches ModeLoopback
	IntentSourceUnloaded IntentSource = "unloaded" // wire-only: no cached node
)

// NodeStatus is the response body for GET /node/{device}/status. Designed
// for newtcon's per-device badges: cheap to populate, no SSH session warmup,
// intent drift count opportunistic — present only when the cached actor
// already has a live device connection.
//
// Topology drift is NOT in this payload (audit finding for issue #75A —
// computing it requires a fresh SSH session inside the actor lock, which
// breaks the "cheap" contract). Callers who want the topology-vs-device
// diff call GET /intent/topology-drift directly.
//
// Mirrors what cached actor state + a non-blocking probe can produce; no
// fields fabricated for the wire (§46).
type NodeStatus struct {
	// Online and OnlineReason classify whether the device's SSH port is
	// reachable. OnlineReason is the canonical newtron.OnlineReason; the
	// browser UI dispatches on this rather than parsing free-form strings.
	Online       bool                 `json:"online"`
	OnlineReason newtron.OnlineReason `json:"online_reason"`

	// HasUnsavedIntents reports Node.HasUnsavedIntents() if the actor has a
	// cached node; false otherwise (no cached state = nothing unsaved).
	HasUnsavedIntents bool `json:"has_unsaved_intents"`

	// IntentSource describes what the cached projection was built from, or
	// "unloaded" when no node is cached yet.
	IntentSource IntentSource `json:"intent_source"`

	// IntentDriftCount is the number of diff entries between the projection
	// (built from cached intents) and the device CONFIG_DB. Populated only
	// when the cached actor already has a live device connection — see
	// IntentDriftReason. Honors the "cheap, no SSH" contract of /status.
	IntentDriftCount  int    `json:"intent_drift_count"`
	IntentDriftReason string `json:"intent_drift_reason,omitempty"`
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

// AlreadyRegisteredErrorInfo is the data payload of the 409 envelope on
// POST /networks when the id is already registered. It carries the existing
// spec_dir so clients can distinguish true-idempotent re-registration (same
// id, same spec_dir → return nil) from a real conflict (same id, different
// spec_dir → surface a typed error). §46 (HTTP API Boundary) on the failure
// path: the substrate the server already knows is propagated through
// envelope.Data, the same shape VerificationFailedError uses for its
// VerificationResult.
type AlreadyRegisteredErrorInfo struct {
	ID              string `json:"id"`
	ExistingSpecDir string `json:"existing_spec_dir"`
}

// alreadyRegisteredError is returned when a network ID is already registered.
type alreadyRegisteredError struct {
	id              string
	existingSpecDir string
}

func (e *alreadyRegisteredError) Error() string {
	if e.existingSpecDir == "" {
		return "network '" + e.id + "' already registered"
	}
	return "network '" + e.id + "' already registered with spec_dir '" + e.existingSpecDir + "'"
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

	var conflict *newtron.ConflictError
	if errors.As(err, &conflict) {
		return http.StatusConflict
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}

	return http.StatusInternalServerError
}
