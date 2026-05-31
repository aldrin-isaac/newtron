package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/version"
)

var (
	verboseFlag      bool
	newtrunServerFlag string // root persistent flag — newtrun-server URL
)

// Sentinel errors for exit code mapping. RunE handlers return these instead
// of calling os.Exit directly, so deferred cleanup (like lock release) runs.
var (
	errTestFailure = errors.New("test failure")
	errInfraError  = errors.New("infrastructure error")
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "newtrun",
		Short: "E2E testing for newtron",
		Long: `Newtrun runs end-to-end test scenarios against newtron-managed topologies.

A suite is a directory of YAML scenario files (e.g., "2node-ngdp-incremental").
Each scenario defines steps like provision, configure, and verify.
Suites can be specified by name (resolved under newtrun/suites/) or by path.

Lifecycle:
  newtrun start <suite>              # deploy topology, run all scenarios
  newtrun status                     # check progress
  newtrun pause                      # stop after current scenario
  newtrun start <suite>              # resume from where it left off
  newtrun stop                       # tear down topology and clean state

Discovery:
  newtrun list                       # show available suites
  newtrun list <suite>               # show scenarios in a suite
  newtrun topologies                 # show available topologies`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	}

	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "Verbose output")
	rootCmd.PersistentFlags().StringVar(&newtrunServerFlag, "newtrun-server", "", "newtrun-server URL (env: NEWTRUN_SERVER; default: http://127.0.0.1:18081)")

	startCmd := newStartCmd()

	rootCmd.AddCommand(
		startCmd,
		newPauseCmd(),
		newStopCmd(),
		newStatusCmd(),
		newListCmd(),
		newSuitesCmd(),
		newSuiteCmd(),
		newScenarioCmd(),
		newTopologiesCmd(),
		newActionsCmd(),
		&cobra.Command{
			Use:   "version",
			Short: "Print version information",
			Run: func(cmd *cobra.Command, args []string) {
				if version.Version == "dev" {
					fmt.Println("newtrun dev build (use 'make build' for version info)")
				} else {
					fmt.Printf("newtrun %s (%s)\n", version.Version, version.GitCommit)
				}
			},
		},
	)

	if err := rootCmd.Execute(); err != nil {
		// Print the error message in both paths — the previous code only
		// printed it for exit 1, so infra-error exits (server connection
		// lost, run aborted) left the operator with a bare exit 2 and no
		// hint about which failure mode it was.
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errInfraError) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
