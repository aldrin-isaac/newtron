package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/version"
)

var verboseFlag bool

// Sentinel errors for exit code mapping. RunE handlers return these instead
// of calling os.Exit directly, so deferred cleanup (like lock release) runs.
var (
	errTestFailure = errors.New("test failure")
	errInfraError  = errors.New("infrastructure error")
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "newtest",
		Short: "E2E testing for newtron",
		Long: `Newtest runs end-to-end test scenarios against newtron-managed topologies.

A suite is a directory of YAML scenario files (e.g., "2node-incremental").
Each scenario defines steps like provision, configure, and verify.
Suites can be specified by name (resolved under newtest/suites/) or by path.

Lifecycle:
  newtest start <suite>              # deploy topology, run all scenarios
  newtest status                     # check progress
  newtest pause                      # stop after current scenario
  newtest start <suite>              # resume from where it left off
  newtest stop                       # tear down topology and clean state

Discovery:
  newtest list                       # show available suites
  newtest list <suite>               # show scenarios in a suite
  newtest topologies                 # show available topologies`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	}

	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "Verbose output")

	startCmd := newStartCmd()

	// Register "run" as a hidden alias for "start" (backward compatibility)
	runCmd := *startCmd
	runCmd.Use = "run [suite]"
	runCmd.Hidden = true
	runCmd.Deprecated = "use 'start' instead"

	rootCmd.AddCommand(
		startCmd,
		newPauseCmd(),
		newStopCmd(),
		newStatusCmd(),
		&runCmd,
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
		if errors.Is(err, errInfraError) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
