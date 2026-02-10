package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available scenarios",
		RunE: func(cmd *cobra.Command, args []string) error {
			scenarios, err := newtest.ParseAllScenarios("newtest/suites/2node-standalone")
			if err != nil {
				return err
			}
			if len(scenarios) == 0 {
				fmt.Println("No scenarios found in newtest/suites/2node-standalone/")
				return nil
			}
			fmt.Println("Available scenarios:")
			for _, s := range scenarios {
				fmt.Printf("  %-20s %s (%s, %s)\n",
					s.Name, s.Description, s.Topology, s.Platform)
			}
			return nil
		},
	}
}
