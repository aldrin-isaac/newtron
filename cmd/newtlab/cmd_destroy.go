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
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			labName, err := resolveLabName(args)
			if err != nil {
				return err
			}

			fmt.Printf("Destroying lab %s...\n", labName)
			if err := newtlab.DestroyByName(cmd.Context(), labName); err != nil {
				return err
			}
			fmt.Printf("%s Lab %s destroyed\n", green("✓"), labName)
			return nil
		},
	}
	return cmd
}
