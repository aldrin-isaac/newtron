package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy [topology]",
		Short: "Stop and remove all VMs",
		Long: `Tear down a deployed lab completely.

Kills all QEMU processes, removes overlay disks, and cleans up state.
If only one lab is deployed, the topology name can be omitted.

  newtlab destroy 2node
  newtlab destroy              # auto-selects if only one lab`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			labName, err := resolveLabName(args)
			if err != nil {
				return err
			}

			fmt.Printf("Destroying lab %s...\n", labName)
			lab := &newtlab.Lab{Name: labName}
			lab.OnProgress = func(phase, detail string) {
				fmt.Printf("  [%s] %s\n", phase, detail)
			}
			if err := lab.Destroy(cmd.Context()); err != nil {
				return err
			}
			fmt.Printf("%s Lab %s destroyed\n", green("✓"), labName)
			return nil
		},
	}
	return cmd
}
