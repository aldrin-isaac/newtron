// Package api implements the newtlab HTTP server.
//
// The server exposes newtlab's existing canonical types (LabState,
// NodeState, LinkState from pkg/newtlab) over HTTP so consumers like
// the newtcon browser frontend can deploy, destroy, and observe labs
// without dropping to a shell.
//
// Per DESIGN_PRINCIPLES_NEWTRON.md §46 (Wire Shape Mirrors Canonical
// Types), the HTTP responses serialize the canonical in-memory types
// directly. This file declares the request/response envelopes and the
// SSE event shapes; per-resource fields come from pkg/newtlab/state.go.
package api

import (
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// HealthResponse is the GET /api/health payload.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// LabListItem is one entry in GET /api/labs. The list comes from
// newtlab.ListLabs() — names of every lab with a state directory,
// running or not. Use GET /api/labs/{name}/status to determine whether
// the lab is currently up.
type LabListItem struct {
	Name string `json:"name"`
}

// DeployRequest is the optional POST /api/labs/{name}/deploy body. All
// fields default to zero (no provision, no host filter). Operators that
// need the equivalent of `bin/newtlab deploy --provision` set
// Provision = true.
type DeployRequest struct {
	// Provision, when true, runs newtlab's post-deploy provisioning pass
	// after the VMs boot. Equivalent to passing --provision to the CLI.
	Provision bool `json:"provision,omitempty"`

	// Force, when true, destroys any existing deployment of the same lab
	// before starting. Equivalent to --force on the CLI. Without it,
	// deploying onto a running lab returns 409 Conflict.
	Force bool `json:"force,omitempty"`

	// Host filters deployment to the named newtlab host (multi-host
	// labs). Empty = deploy on all hosts.
	Host string `json:"host,omitempty"`

	// Parallel sets the parallelism for the provisioning pass (only
	// applied when Provision is true). Zero = newtlab default (1).
	Parallel int `json:"parallel,omitempty"`
}

// DeployResponse is returned by POST /api/labs/{name}/deploy with HTTP
// 202. The deploy runs asynchronously; subscribe to
// /api/labs/{name}/events for phase events, or poll
// /api/labs/{name}/status to observe terminal state.
type DeployResponse struct {
	Lab     string `json:"lab"`
	Started string `json:"started"` // RFC3339
}

// StatusResponse mirrors newtlab.LabState directly. Returning the
// canonical type via embedding keeps the wire shape source-of-truth in
// pkg/newtlab/state.go (DESIGN_PRINCIPLES_NEWTRON.md §46).
type StatusResponse struct {
	*newtlab.LabState
}

// EventType identifies the SSE event kind emitted on
// /api/labs/{name}/events. The set mirrors the `phase, detail` pairs
// newtlab.Lab.OnProgress emits during Deploy and Destroy. Unknown
// phases pass through as EventPhase with the raw phase string in the
// payload, so SSE consumers never miss a tick even if newtlab adds a
// new phase tomorrow.
type EventType string

const (
	// EventPhase is the generic deploy / destroy progress event. The
	// payload carries the phase name and free-form detail. Newtcon
	// renders these as a rolling status line during deploy.
	EventPhase EventType = "phase"

	// EventComplete is emitted by the deploy goroutine when the
	// operation finishes successfully. Payload is empty.
	EventComplete EventType = "complete"

	// EventError is emitted by the deploy goroutine when the operation
	// fails. Payload carries the error message.
	EventError EventType = "error"
)

// Event is one SSE event the server emits on
// GET /api/labs/{name}/events. Type discriminates Payload's concrete
// shape. Implements httputil.Eventable so the generic SSE stream writer
// can emit Type as the SSE `event:` token and Payload as the `data:`
// JSON body.
type Event struct {
	Type    EventType `json:"type"`
	Payload any       `json:"payload"`
}

// Kind satisfies httputil.Eventable — returns the SSE event-type token.
func (e Event) Kind() string { return string(e.Type) }

// Body satisfies httputil.Eventable — returns the value to JSON-encode
// after the SSE `data:` prefix.
func (e Event) Body() any { return e.Payload }

// PhasePayload is the payload for EventPhase. Mirrors the
// `phase, detail` arguments of newtlab.Lab.OnProgress directly.
type PhasePayload struct {
	Phase  string `json:"phase"`
	Detail string `json:"detail,omitempty"`
}

// ErrorPayload is the payload for EventError. The deploy / destroy
// goroutine sets Message to the returned error's Error() string.
type ErrorPayload struct {
	Message string `json:"message"`
}
