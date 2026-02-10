package main

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
)

func newRunCmd() *cobra.Command {
	var opts newtest.RunOptions
	var dir string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run test scenarios",
		RunE: func(cmd *cobra.Command, args []string) error {
			runner := newtest.NewRunner(dir, "newtest/topologies")
			results, err := runner.Run(opts)
			if err != nil {
				return err
			}

			// Print console output
			gen := &newtest.ReportGenerator{Results: results}
			gen.PrintConsole(os.Stdout)

			// Write markdown report
			_ = gen.WriteMarkdown("newtest/.generated/report.md")

			// Write JUnit if requested
			if opts.JUnitPath != "" {
				_ = gen.WriteJUnit(opts.JUnitPath)
			}

			// Exit code based on results
			// Exit 2 = infra error, Exit 1 = test failure
			hasFailure, hasInfraError := false, false
			for _, r := range results {
				if r.DeployError != nil {
					var infraErr *newtest.InfraError
					if errors.As(r.DeployError, &infraErr) {
						hasInfraError = true
					} else {
						hasInfraError = true
					}
				}
				if r.Status == newtest.StatusError {
					hasInfraError = true
				}
				if r.Status == newtest.StatusFailed {
					hasFailure = true
				}
			}
			if hasInfraError {
				os.Exit(2)
			}
			if hasFailure {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "newtest/scenarios", "directory containing scenario YAML files")
	cmd.Flags().StringVar(&opts.Scenario, "scenario", "", "run specific scenario")
	cmd.Flags().BoolVar(&opts.All, "all", false, "run all scenarios in dir")
	cmd.Flags().StringVar(&opts.Topology, "topology", "", "override topology")
	cmd.Flags().StringVar(&opts.Platform, "platform", "", "override platform")
	cmd.Flags().BoolVar(&opts.Keep, "keep", false, "don't destroy topology after tests")
	cmd.Flags().BoolVar(&opts.NoDeploy, "no-deploy", false, "skip deploy/destroy")
	cmd.Flags().IntVar(&opts.Parallel, "parallel", 1, "parallel provisioning count")
	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "verbose output")
	cmd.Flags().StringVar(&opts.JUnitPath, "junit", "", "JUnit XML output path")

	return cmd
}
