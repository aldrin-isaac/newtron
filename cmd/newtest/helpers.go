package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtest"
	"github.com/newtron-network/newtron/pkg/settings"
)

// resolveDir resolves the suite directory from: positional arg > flag > env > settings > default.
// A bare name like "2node-incremental" is resolved under the suites base directory.
func resolveDir(cmd *cobra.Command, flagVal string, args ...string) string {
	// Positional arg takes priority
	if len(args) > 0 && args[0] != "" {
		return resolveSuiteName(args[0])
	}
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

// resolveSuiteName resolves a suite name to a directory path.
// If name is already a path (contains /), use it directly.
// Otherwise, look under newtest/suites/<name>.
func resolveSuiteName(name string) string {
	// Already a path
	if strings.Contains(name, "/") {
		return name
	}
	// Try under suites base
	candidate := filepath.Join(suitesBaseDir(), name)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	// Fall through: return as-is and let downstream report the error
	return name
}

// suitesBaseDir returns the base directory for suites: env > settings > default.
func suitesBaseDir() string {
	if v := os.Getenv("NEWTEST_SUITES_BASE"); v != "" {
		return v
	}
	return "newtest/suites"
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

// resolveSuite resolves a suite name from --dir flag or auto-detection.
// The filter function controls which suites are considered: return true for
// suites that should be included. Pass nil to accept any suite with state.
func resolveSuite(cmd *cobra.Command, dir string, filter func(newtest.SuiteStatus) bool) (string, error) {
	if cmd.Flags().Changed("dir") {
		return newtest.SuiteName(dir), nil
	}

	suites, err := newtest.ListSuiteStates()
	if err != nil {
		return "", err
	}

	if len(suites) == 0 {
		return "", fmt.Errorf("no active suite found; use --dir to specify")
	}

	// No filter: accept any suite with state (e.g. stop command)
	if filter == nil {
		if len(suites) > 1 {
			return "", fmt.Errorf("multiple active suites: %v; use --dir to specify", suites)
		}
		return suites[0], nil
	}

	// Apply filter (e.g. pause command wants only running/pausing/paused)
	var matched []string
	for _, s := range suites {
		state, err := newtest.LoadRunState(s)
		if err != nil || state == nil {
			continue
		}
		if filter(state.Status) {
			matched = append(matched, s)
		}
	}

	if len(matched) == 0 {
		return "", fmt.Errorf("no active suite found; use --dir to specify")
	}
	if len(matched) > 1 {
		return "", fmt.Errorf("multiple active suites: %v; use --dir to specify", matched)
	}
	return matched[0], nil
}

// resolveTopologyFromState infers the topology name from suite state.
// Falls back to parsing scenario files if state.Topology is empty.
func resolveTopologyFromState(state *newtest.RunState) string {
	if state.Topology != "" {
		return state.Topology
	}
	if state.SuiteDir != "" {
		scenarios, _ := newtest.ParseAllScenarios(state.SuiteDir)
		if len(scenarios) > 0 {
			return scenarios[0].Topology
		}
	}
	return ""
}
