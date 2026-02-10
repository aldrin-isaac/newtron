package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/version"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "newtest",
		Short: "E2E testing for newtron",
	}

	rootCmd.AddCommand(
		newRunCmd(),
		newListCmd(),
		newTopologiesCmd(),
		&cobra.Command{
			Use:   "version",
			Short: "Print version information",
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Printf("newtest %s (%s)\n", version.Version, version.GitCommit)
			},
		},
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
