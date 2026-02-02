//go:build e2e

package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.InitReport()

	// Reset all SONiC nodes to baseline config before running tests.
	// This clears stale CONFIG_DB entries from previous test runs that
	// could crash vxlanmgrd or confuse orchagent.
	fmt.Fprintf(os.Stderr, "Resetting lab to baseline config...\n")
	if err := testutil.ResetLabBaseline(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: baseline reset: %v\n", err)
	}

	// Ensure fabric infrastructure is configured on all SONiC nodes AFTER
	// baseline reset. This pushes underlay BGP, fabric link IPs, VTEP, and
	// EVPN config from the generated config_db.json to the running CONFIG_DB.
	// ResetLabBaseline may delete stale entries; this restores infrastructure.
	fmt.Fprintf(os.Stderr, "Ensuring fabric startup config on all nodes...\n")
	if err := testutil.EnsureStartupConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: startup config push: %v\n", err)
	}

	code := m.Run()
	testutil.CloseLabTunnels()
	reportPath := filepath.Join(testutil.ProjectRoot(), "testlab", ".generated", "e2e-report.md")
	if err := testutil.WriteReport(reportPath); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: failed to write E2E report: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "E2E report written to %s\n", reportPath)
	}
	os.Exit(code)
}
