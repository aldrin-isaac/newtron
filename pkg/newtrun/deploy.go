package newtrun

import (
	"context"
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
	"github.com/aldrin-isaac/newtron/pkg/newtlab/api"
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
)

// LabClient is the subset of pkg/newtlab/client.Client newtrun calls
// during deploy / destroy / status. The interface exists for unit
// testing — production code passes *newtlabclient.Client directly,
// which satisfies it structurally. Per §27 (Single Owner) every
// touch of newtlab-owned state (LabState, deploy lifecycle) routes
// through newtlab-server's HTTP surface, not in-process via
// newtlab.NewLab from the runner process.
type LabClient interface {
	LabStatus(ctx context.Context, lab string) (*newtlab.LabState, error)
	Deploy(ctx context.Context, lab string, opts api.DeployRequest) error
	Destroy(ctx context.Context, lab string) error
}

// Ensure: *newtlabclient.Client satisfies LabClient.
var _ LabClient = (*newtlabclient.Client)(nil)

// DeployTopology deploys the named VM topology by calling newtlab-
// server. Spec data flows from newtron via newtlab-server's own
// SpecClient (§27 — newtron owns spec files, newtlab owns lab
// state); newtrun stays a client of both.
func DeployTopology(ctx context.Context, client LabClient, topologyName string) error {
	if err := client.Deploy(ctx, topologyName, api.DeployRequest{Force: true}); err != nil {
		return fmt.Errorf("newtrun: deploy topology: %w", err)
	}
	return nil
}

// EnsureTopology reuses an existing lab if all nodes are running,
// otherwise deploys fresh. The status check is best-effort — a 404
// (topology not deployed) or a partial-running state falls through
// to a forced redeploy.
func EnsureTopology(ctx context.Context, client LabClient, topologyName string) error {
	if state, err := client.LabStatus(ctx, topologyName); err == nil && allNodesRunning(state) {
		return nil
	}
	return DeployTopology(ctx, client, topologyName)
}

// DestroyTopology tears down the named topology.
func DestroyTopology(ctx context.Context, client LabClient, topologyName string) error {
	if err := client.Destroy(ctx, topologyName); err != nil {
		return fmt.Errorf("newtrun: destroy topology: %w", err)
	}
	return nil
}

// allNodesRunning reports whether the LabState has at least one node
// and every node is in the "running" status. EnsureTopology uses it
// to skip a redeploy when the lab is already up.
func allNodesRunning(state *newtlab.LabState) bool {
	if state == nil || len(state.Nodes) == 0 {
		return false
	}
	for _, node := range state.Nodes {
		if node.Status != "running" {
			return false
		}
	}
	return true
}
