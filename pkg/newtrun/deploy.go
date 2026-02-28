package newtrun

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

// DeployTopology deploys a VM topology using newtlab.
func DeployTopology(ctx context.Context, specDir string) (*newtlab.Lab, error) {
	lab, err := newtlab.NewLab(specDir)
	if err != nil {
		return nil, fmt.Errorf("newtrun: load topology: %w", err)
	}
	lab.Force = true
	if err := lab.Deploy(ctx); err != nil {
		return nil, fmt.Errorf("newtrun: deploy topology: %w", err)
	}
	return lab, nil
}

// EnsureTopology reuses an existing lab if all nodes are running, otherwise
// deploys fresh.
func EnsureTopology(ctx context.Context, specDir string) (*newtlab.Lab, error) {
	lab, err := newtlab.NewLab(specDir)
	if err != nil {
		return nil, fmt.Errorf("newtrun: load topology: %w", err)
	}

	// Reuse if all nodes running
	if state, err := lab.Status(); err == nil && len(state.Nodes) > 0 {
		allRunning := true
		for _, node := range state.Nodes {
			if node.Status != "running" {
				allRunning = false
				break
			}
		}
		if allRunning {
			return lab, nil
		}
	}

	lab.Force = true
	if err := lab.Deploy(ctx); err != nil {
		return nil, fmt.Errorf("newtrun: deploy topology: %w", err)
	}
	return lab, nil
}

// DestroyTopology tears down a deployed topology.
func DestroyTopology(ctx context.Context, lab *newtlab.Lab) error {
	if lab == nil {
		return nil
	}
	if err := lab.Destroy(ctx); err != nil {
		return fmt.Errorf("newtrun: destroy topology: %w", err)
	}
	return nil
}
