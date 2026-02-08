package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newTopologiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "topologies",
		Short: "List available topologies",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := os.ReadDir("newtest/topologies")
			if err != nil {
				return err
			}
			fmt.Println("Available topologies:")
			for _, e := range entries {
				if e.IsDir() {
					fmt.Printf("  %s\n", e.Name())
				}
			}
			return nil
		},
	}
}
