package main

import (
	"github.com/spf13/cobra"
)

func newSuitesCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "suites",
		Short:  "List available test suites (alias for 'list')",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listSuites()
		},
	}
}
