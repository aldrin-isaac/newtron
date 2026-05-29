package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSuitesCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "suites",
		Short:  "List available suites (alias for 'list --suites')",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			names, err := c.ListSuites(ctx)
			if err != nil {
				return err
			}
			for _, name := range names {
				fmt.Println(name)
			}
			return nil
		},
	}
}
