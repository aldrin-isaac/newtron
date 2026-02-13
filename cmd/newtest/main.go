package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/version"
)

var verboseFlag bool

func main() {
	rootCmd := &cobra.Command{
		Use:   "newtest",
		Short: "E2E testing for newtron",
		Long: `Newtest runs end-to-end test scenarios against newtron-managed topologies.

Scenarios are YAML files that define steps (provision, configure, verify).

  newtest list                       # show available suites
  newtest list 2node-incremental     # show scenarios in a suite
  newtest start 2node-incremental    # deploy topology and run all scenarios
  newtest status                     # check progress
  newtest pause                      # stop after current scenario
  newtest stop                       # tear down topology`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	}

	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "Verbose output")

	rootCmd.AddCommand(
		newStartCmd(),
		newPauseCmd(),
		newStopCmd(),
		newStatusCmd(),
		newRunCmd(),
		newListCmd(),
		newSuitesCmd(),
		newTopologiesCmd(),
		&cobra.Command{
			Use:   "version",
			Short: "Print version information",
			Run: func(cmd *cobra.Command, args []string) {
				if version.Version == "dev" {
					fmt.Println("newtest dev build (use 'make build' for version info)")
				} else {
					fmt.Printf("newtest %s (%s)\n", version.Version, version.GitCommit)
				}
			},
		},
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
