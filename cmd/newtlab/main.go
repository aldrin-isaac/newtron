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

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/settings"
	"github.com/newtron-network/newtron/pkg/util"
	"github.com/newtron-network/newtron/pkg/version"
)

var (
	specDir string
	verbose bool
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:               "newtlab",
	Short:             "VM orchestration for SONiC network topologies",
	SilenceUsage:      true,
	SilenceErrors:     true,
	CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	Long: `NewtLab deploys QEMU virtual machines from newtron spec files.

It reads topology.json, platforms.json, and profiles/*.json to create
connected VMs with socket-based networking. No root, no bridges, no Docker.

  newtlab deploy -S <specs>`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if verbose {
			util.SetLogLevel("debug")
		} else {
			util.SetLogLevel("warn")
		}
		return nil
	},
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
		newVersionCmd(),
	)
}

// requireSpecDir resolves spec directory from: -S flag > NEWTLAB_SPECS env > settings > error.
func requireSpecDir() (string, error) {
	if specDir != "" {
		return specDir, nil
	}
	if v := os.Getenv("NEWTLAB_SPECS"); v != "" {
		return v, nil
	}
	if s, err := settings.Load(); err == nil && s.SpecDir != "" {
		return s.SpecDir, nil
	}
	return "", fmt.Errorf("spec directory required: use -S <dir>, set NEWTLAB_SPECS, or run 'newtron settings set specs <dir>'")
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			if version.Version == "dev" {
				fmt.Println("newtlab dev build (use 'make build' for version info)")
			} else {
				fmt.Printf("newtlab %s (%s)\n", version.Version, version.GitCommit)
			}
		},
	}
}

// Color helpers — delegate to pkg/cli
func green(s string) string  { return cli.Green(s) }
func yellow(s string) string { return cli.Yellow(s) }
func red(s string) string    { return cli.Red(s) }
