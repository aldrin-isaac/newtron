package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Stop and remove all VMs",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine lab name: from spec dir or from existing labs
			var labName string
			if specDir != "" {
				lab, err := newtlab.NewLab(specDir)
				if err != nil {
					return err
				}
				labName = lab.Name
			} else {
				// Try to find exactly one lab
				labs, err := newtlab.ListLabs()
				if err != nil {
					return err
				}
				if len(labs) == 0 {
					return fmt.Errorf("no labs found")
				}
				if len(labs) > 1 {
					return fmt.Errorf("multiple labs found, specify with -S: %v", labs)
				}
				labName = labs[0]
			}

			lab := &newtlab.Lab{Name: labName}
			fmt.Printf("Destroying lab %s...\n", labName)
			if err := lab.Destroy(); err != nil {
				return err
			}
			fmt.Printf("%s Lab %s destroyed\n", green("✓"), labName)
			return nil
		},
	}
	return cmd
}
