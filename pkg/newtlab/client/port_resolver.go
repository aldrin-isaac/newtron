package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// NotInLabError is returned by SSHPort when the lab is not realised —
// either newtlab-server has no LabState for the named lab (the lab
// isn't deployed) or the lab exists but doesn't contain the named
// device. Distinct from transport errors, so consumers like newtron's
// /status endpoint can render "lab not realised" separately from
// "newtlab unreachable" without parsing error message strings (replaces
// a fragile substring match in pkg/newtron/network.go).
type NotInLabError struct {
	Lab    string
	Device string // may be "" when the lab itself isn't deployed
}

func (e *NotInLabError) Error() string {
	if e.Device == "" {
		return fmt.Sprintf("newtlab lab %q is not deployed", e.Lab)
	}
	return fmt.Sprintf("device %q not in newtlab lab %q", e.Device, e.Lab)
}

// PortResolverNotReady is the marker method that satisfies
// sonic.NotReadyError. newtron consumers classify the error via
// errors.As against that interface, keeping pkg/newtron free of any
// compile-time dependency on this package. No arguments, no return —
// satisfying the interface IS the entire signal.
func (e *NotInLabError) PortResolverNotReady() {}

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
// named lab. lab is the path segment in
// GET /newtlab/v1/labs/{name}/status.
//
// Returns *NotInLabError when LabStatus 404s (lab not deployed) or when
// the device isn't in the deployed lab — that error class is what
// newtron's /status endpoint dispatches on. Other failures (transport,
// 5xx) are wrapped server errors.
func (r *PortResolver) SSHPort(ctx context.Context, lab, device string) (int, error) {
	state, err := r.client.LabStatus(ctx, lab)
	if err != nil {
		var se *ServerError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return 0, &NotInLabError{Lab: lab}
		}
		return 0, fmt.Errorf("newtlab LabStatus(%q): %w", lab, err)
	}
	node, ok := state.Nodes[device]
	if !ok {
		return 0, &NotInLabError{Lab: lab, Device: device}
	}
	return node.SSHPort, nil
}
