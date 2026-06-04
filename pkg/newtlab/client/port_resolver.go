package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// NotInTopologyError is returned by SSHPort when the lab topology is not
// realised — either newtlab-server has no LabState for the named topology
// (the lab isn't deployed) or the topology exists but doesn't contain the
// named device. Distinct from transport errors, so consumers like newtron's
// /status endpoint can render "topology not realised" separately from
// "newtlab unreachable" without parsing error message strings (replaces a
// fragile substring match in pkg/newtron/network.go).
type NotInTopologyError struct {
	Topology string
	Device   string // may be "" when the topology itself isn't deployed
}

func (e *NotInTopologyError) Error() string {
	if e.Device == "" {
		return fmt.Sprintf("newtlab topology %q is not deployed", e.Topology)
	}
	return fmt.Sprintf("device %q not in newtlab topology %q", e.Device, e.Topology)
}

// PortResolverNotReady is the marker method that satisfies
// sonic.NotReadyError. newtron consumers classify the error via
// errors.As against that interface, keeping pkg/newtron free of any
// compile-time dependency on this package. No arguments, no return —
// satisfying the interface IS the entire signal.
func (e *NotInTopologyError) PortResolverNotReady() {}

// PortResolver answers per-device runtime port questions by consulting
// newtlab-server's LabState. Structurally satisfies the contract any
// consumer would declare (e.g. newtron's sonic.PortResolver), so cmd
// wiring can inject this into newtron without either engine importing
// the other (DESIGN_PRINCIPLES §33, §34).
type PortResolver struct {
	client *Client
}

// NewPortResolver constructs a resolver backed by a newtlab client.
func NewPortResolver(c *Client) *PortResolver {
	return &PortResolver{client: c}
}

// SSHPort returns the SSH port allocated for the named device in the
// named topology. Topology is the path segment in
// GET /newtlab/v1/topologies/{name}/status.
//
// Returns *NotInTopologyError when LabStatus 404s (topology not deployed)
// or when the device isn't in the deployed topology — that error class is
// what newtron's /status endpoint dispatches on. Other failures (transport,
// 5xx) are wrapped server errors.
func (r *PortResolver) SSHPort(ctx context.Context, topology, device string) (int, error) {
	state, err := r.client.LabStatus(ctx, topology)
	if err != nil {
		var se *ServerError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return 0, &NotInTopologyError{Topology: topology}
		}
		return 0, fmt.Errorf("newtlab LabStatus(%q): %w", topology, err)
	}
	node, ok := state.Nodes[device]
	if !ok {
		return 0, &NotInTopologyError{Topology: topology, Device: device}
	}
	return node.SSHPort, nil
}
