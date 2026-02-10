package main

import (
	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newBridgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "bridge <lab-name>",
		Short:  "Run bridge workers (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return newtlab.RunBridge(args[0])
		},
	}
	return cmd
}
