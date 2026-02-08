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
	if err := lab.Deploy(); err != nil {
		return nil, fmt.Errorf("newtest: deploy topology: %w", err)
	}
	return lab, nil
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
