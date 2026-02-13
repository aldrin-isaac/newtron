package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
