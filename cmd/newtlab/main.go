// NewtLab — VM orchestration for SONiC network topologies
//
// newtlab reads newtron's spec files (topology.json, platforms.json,
// profiles/*.json) and deploys QEMU virtual machines with socket-based
// networking. No root, no bridges, no Docker.
//
// Usage:
//
//	newtlab deploy -S <specs>        Deploy VMs from topology.json
//	newtlab destroy                  Stop and remove all VMs
//	newtlab status                   Show VM status
//	newtlab ssh <node>               SSH to a VM
//	newtlab console <node>           Attach to serial console
//	newtlab stop <node>              Stop a VM (preserves disk)
//	newtlab start <node>             Start a stopped VM
//	newtlab provision -S <specs>     Provision devices via newtron
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	specDir string
	verbose bool
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "newtlab",
	Short: "VM orchestration for SONiC network topologies",
	Long: `NewtLab deploys QEMU virtual machines from newtron spec files.

It reads topology.json, platforms.json, and profiles/*.json to create
connected VMs with socket-based networking. No root, no bridges, no Docker.

Commands:
  deploy      Deploy VMs from topology.json
  destroy     Stop and remove all VMs
  status      Show VM status
  ssh         SSH to a VM
  console     Attach to serial console
  stop/start  Stop or start individual VMs
  provision   Provision devices via newtron`,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&specDir, "specs", "S", "", "spec directory")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	rootCmd.AddCommand(
		newDeployCmd(),
		newDestroyCmd(),
		newStatusCmd(),
		newSSHCmd(),
		newConsoleCmd(),
		newStopCmd(),
		newStartCmd(),
		newProvisionCmd(),
		newBridgeCmd(),
		newBridgeStatsCmd(),
	)
}

// requireSpecDir returns specDir or errors if not set.
func requireSpecDir() (string, error) {
	if specDir == "" {
		return "", fmt.Errorf("spec directory required: use -S <dir>")
	}
	return specDir, nil
}

// Color helpers for terminal output
func green(s string) string  { return "\033[32m" + s + "\033[0m" }
func yellow(s string) string { return "\033[33m" + s + "\033[0m" }
func red(s string) string    { return "\033[31m" + s + "\033[0m" }
