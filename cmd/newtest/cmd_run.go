package main

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
	"github.com/newtron-network/newtron/pkg/settings"
)

func newRunCmd() *cobra.Command {
	var opts newtest.RunOptions
	var dir string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run test scenarios",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir = resolveDir(cmd, dir)
			topologiesDir := resolveTopologiesDir()

			runner := newtest.NewRunner(dir, topologiesDir)
			runner.Progress = newtest.NewConsoleProgress(opts.Verbose)
			results, err := runner.Run(opts)
			if err != nil {
				return err
			}

			// Write markdown report
			gen := &newtest.ReportGenerator{Results: results}
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

	cmd.Flags().StringVar(&dir, "dir", "", "directory containing scenario YAML files")
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

// resolveDir resolves the suite directory from: flag > env > settings > default.
func resolveDir(cmd *cobra.Command, flagVal string) string {
	if cmd.Flags().Changed("dir") {
		return flagVal
	}
	if v := os.Getenv("NEWTEST_SUITE"); v != "" {
		return v
	}
	if s, err := settings.Load(); err == nil && s.DefaultSuite != "" {
		return s.DefaultSuite
	}
	return "newtest/suites/2node-standalone"
}

// resolveTopologiesDir resolves the topologies base directory from: env > settings > default.
func resolveTopologiesDir() string {
	if v := os.Getenv("NEWTEST_TOPOLOGIES"); v != "" {
		return v
	}
	if s, err := settings.Load(); err == nil && s.TopologiesDir != "" {
		return s.TopologiesDir
	}
	return "newtest/topologies"
}
