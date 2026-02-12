package newtest

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

// DeployTopology deploys a VM topology using newtlab.
func DeployTopology(specDir string) (*newtlab.Lab, error) {
	lab, err := newtlab.NewLab(specDir)
	if err != nil {
		return nil, fmt.Errorf("newtest: load topology: %w", err)
	}
	lab.Force = true
	if err := lab.Deploy(); err != nil {
		return nil, fmt.Errorf("newtest: deploy topology: %w", err)
	}
	return lab, nil
}

// EnsureTopology reuses an existing lab if all nodes are running, otherwise
// deploys fresh. Returns the lab and whether a new deploy was performed.
func EnsureTopology(specDir string) (*newtlab.Lab, bool, error) {
	lab, err := newtlab.NewLab(specDir)
	if err != nil {
		return nil, false, fmt.Errorf("newtest: load topology: %w", err)
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
			return lab, false, nil
		}
	}

	lab.Force = true
	if err := lab.Deploy(); err != nil {
		return nil, false, fmt.Errorf("newtest: deploy topology: %w", err)
	}
	return lab, true, nil
}

// DestroyTopology tears down a deployed topology.
func DestroyTopology(lab *newtlab.Lab) error {
	if lab == nil {
		return nil
	}
	if err := lab.Destroy(); err != nil {
		return fmt.Errorf("newtest: destroy topology: %w", err)
	}
	return nil
}
