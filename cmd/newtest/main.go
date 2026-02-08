package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
