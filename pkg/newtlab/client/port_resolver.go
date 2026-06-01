package client

import (
	"context"
	"fmt"
)

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
func (r *PortResolver) SSHPort(ctx context.Context, topology, device string) (int, error) {
	state, err := r.client.LabStatus(ctx, topology)
	if err != nil {
		return 0, fmt.Errorf("newtlab LabStatus(%q): %w", topology, err)
	}
	node, ok := state.Nodes[device]
	if !ok {
		return 0, fmt.Errorf("device %q not in newtlab topology %q", device, topology)
	}
	return node.SSHPort, nil
}
