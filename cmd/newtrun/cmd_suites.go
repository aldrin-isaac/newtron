package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

func newSuitesCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "suites",
		Short:  "List available suites (alias for 'list --suites')",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			base := suitesBaseDir()
			entries, err := os.ReadDir(base)
			if err != nil {
				return fmt.Errorf("read suites dir %s: %w", base, err)
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				dir := base + "/" + e.Name()
				scenarios, perr := newtrun.ParseAllScenarios(dir)
				if perr != nil || len(scenarios) == 0 {
					continue
				}
				fmt.Println(e.Name())
			}
			return nil
		},
	}
}
