package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ============================================================================
// HTTP Request Types — Network
// ============================================================================

// CreateNetworkRequest is the body for POST /newtron/v1/networks.
//
// The operator names the network by id; the server owns the on-disk
// path (§27, §33). Every call resolves to filepath.Join(networksBase, id)
// on the server's filesystem, where networksBase is whatever the
// operator started newtron with (cmd/newt-server --networks-base;
// default "networks").
//
// Behavior is always idempotent. What happens at <networksBase>/<id>:
//   - id already registered in memory:    200 with existing NetworkInfo
//   - dir doesn't exist:                  server creates empty specs + registers, 201
//   - dir exists, valid layout:           server registers existing, 201
//   - dir exists, invalid layout:         500 load error
//
// Operators distinguish "I just made this" from "it was already here"
// via the response status (201 vs 200) — no client-side flag needed.
//
// Description seeds topology.json's description field when the slot is
// empty and gets created. Ignored on existing slots — no rewrite of
// authored specs.
//
// The resolved absolute dir is returned in the NetworkInfo response so
// callers can display "created at <path>" without re-fetching.
//
// id must match ^[A-Za-z0-9_-]+$, 1–64 characters. Path separators,
// dots, and spaces are rejected at the validation layer; an operator
// who needs them files a bug, not a workaround.
type CreateNetworkRequest struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

// NetworkInfo is returned when listing or showing a registered network.
type NetworkInfo struct {
	ID          string   `json:"id"`
	Dir         string   `json:"dir"`
	HasTopology bool     `json:"has_topology"`
	Topology    string   `json:"topology,omitempty"` // topology name derived from specDir
	Nodes       []string `json:"nodes"`
	// WriteControl summarizes who currently holds the write-control reservation,
	// so a UI shows the holder for every network in the one GET /networks call.
	// Absent (nil) when control is free or expired; full status (incl.
	// last_active) is at GET .../control.
	WriteControl *WriteControlInfo `json:"write_control,omitempty"`
}

// WriteControlInfo is the per-network write-control holder summary carried in
// NetworkInfo.
type WriteControlInfo struct {
	Holder    string    `json:"holder"`
	Since     time.Time `json:"since"`
	ExpiresAt time.Time `json:"expires_at"`
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

// RemoveTrunkVLANRequest is the body for POST .../remove-trunk-vlan.
// The interface name comes from the URL; vlan_id identifies which trunk
// membership to strip. Atomic — other VLANs and the rest of the interface
// configuration are untouched. Issue #224.
type RemoveTrunkVLANRequest struct {
	VLAN int `json:"vlan_id"`
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
// Name addresses the new entry; Device carries the typed TopologyNode as
// stored in topology.json (nodeSpec is implicit via name; Ports + Steps may
// be empty for a bare declaration, or pre-populated for one-shot create).
type TopologyNodeCreateRequest struct {
	Name   string             `json:"name"`
	Device *spec.TopologyNode `json:"device"`
}

// NodeUnbindMACVPNRequest is the body for POST .../unbind-macvpn (node-level).
type NodeUnbindMACVPNRequest struct {
	VlanID int `json:"vlan_id"`
}

// BindQoSRequest is the body for POST .../bind-qos.
type BindQoSRequest struct {
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

// BindIPVPNRequest is the body for POST .../bind-ipvpn. IPVPN is the
// IP-VPN spec name; the on-device SONiC VRF name is derived from it as
// "Vrf_"+name (util.DeriveVRFNameForIPVPN; sonic-vrf.yang / RCA-044).
type BindIPVPNRequest struct {
	IPVPN string `json:"ipvpn"`
}

// StaticRouteRequest is the body for POST .../add-static-route.
type StaticRouteRequest struct {
	VRF     string `json:"vrf"`
	Prefix  string `json:"prefix"`
	NextHop string `json:"nexthop"`
	Metric  int    `json:"metric,omitempty"`
}

// StaticRouteUpdateRequest is the body for POST .../update-static-route.
// Updates fields of an existing static route. The composite key
// (vrf + prefix) is the row's identity (§47) and is not mutable
// through this verb — relocate via remove + add. #227.
type StaticRouteUpdateRequest struct {
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

	// A spec lookup that misses (unknown name, or no override at a requested
	// scope) is a 404 — the internal vocab the spec/network layers return when a
	// kind/name doesn't resolve. Without this it fell through to 500.
	var specNotFound *spec.NotFoundError
	if errors.As(err, &specNotFound) {
		return http.StatusNotFound
	}

	var validation *newtron.ValidationError
	if errors.As(err, &validation) {
		return http.StatusBadRequest
	}

	// Constraint validation from the shared spec validators
	// (util.ValidationBuilder) — a malformed spec rejected at the write boundary,
	// the same check the loader runs. Invalid input, 400.
	var constraintErr *util.ValidationError
	if errors.As(err, &constraintErr) {
		return http.StatusBadRequest
	}

	// Forward dependency check: a created/updated spec references a spec that
	// doesn't exist — invalid input, 400.
	var refErr *spec.ReferenceError
	if errors.As(err, &refErr) {
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

	// Write-control reservation refusal — a non-holder write, or a request when
	// another caller holds it. 409 (consistent with the conflict family); the
	// typed payload distinguishes it for clients.
	var wcErr *newtron.WriteControlError
	if errors.As(err, &wcErr) {
		return http.StatusConflict
	}

	// auth-design.md L3: permission denials become 403. The
	// AuthorizationError type wraps the internal auth.PermissionError
	// so the wire response carries the typed Caller/Permission/
	// Resource shape (§46) and the original errors.Is chain to
	// util.ErrPermissionDenied is preserved.
	var authz *newtron.AuthorizationError
	if errors.As(err, &authz) {
		return http.StatusForbidden
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}

	return http.StatusInternalServerError
}
