// NewtLab — VM orchestration for SONiC network topologies
//
// newtlab deploys QEMU virtual machines from newtron topology specs.
// It reads topology.json, platforms.json, and profiles/*.json to create
// connected VMs with socket-based networking. No root, no bridges, no Docker.
//
// Usage:
//
//	newtlab list                     # show topologies
//	newtlab deploy 2node             # deploy VMs
//	newtlab status 2node             # show VM status
//	newtlab ssh spine1               # SSH to a VM
//	newtlab destroy 2node            # tear down
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtlab"
	"github.com/newtron-network/newtron/pkg/newtron/settings"
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
	Long: `NewtLab deploys QEMU virtual machines from newtron topology specs.

Topologies are resolved by name from newtrun/topologies/.

  newtlab list                       # show topologies
  newtlab deploy 2node               # deploy VMs from topology
  newtlab status [topology]          # show VM status
  newtlab ssh <node>                 # SSH to a VM
  newtlab console <node>             # serial console
  newtlab destroy [topology]         # tear down
  newtlab provision [topology]       # provision via newtron`,
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
	rootCmd.PersistentFlags().StringVarP(&specDir, "specs", "S", "", "spec directory (overrides topology name)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	rootCmd.AddCommand(
		newListCmd(),
		newDeployCmd(),
		newDestroyCmd(),
		newStatusCmd(),
		newSSHCmd(),
		newConsoleCmd(),
		newStopCmd(),
		newStartCmd(),
		newProvisionCmd(),
		newVersionCmd(),
	)
}

// ============================================================================
// Topology Resolution
// ============================================================================

// topologiesBaseDir returns the base directory for topologies.
// Resolution: NEWTRUN_TOPOLOGIES env > settings > default.
func topologiesBaseDir() string {
	if v := os.Getenv("NEWTRUN_TOPOLOGIES"); v != "" {
		return v
	}
	if s, err := settings.Load(); err == nil && s.TopologiesDir != "" {
		return s.TopologiesDir
	}
	return "newtrun/topologies"
}

// resolveTopologyDir resolves a topology name to its spec directory.
// If name contains "/" it's used as-is. Otherwise resolved under topologiesBaseDir.
func resolveTopologyDir(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return filepath.Join(topologiesBaseDir(), name, "specs")
}

// resolveTarget resolves both lab name and spec directory from:
// -S flag > positional topology name > auto-detect from deployed labs.
// This is the shared resolution logic used by resolveSpecDir and resolveLabName.
func resolveTarget(args []string) (labName string, dir string, err error) {
	// Explicit -S flag takes priority
	if specDir != "" {
		lab, labErr := newtlab.NewLab(specDir)
		if labErr != nil {
			return "", "", labErr
		}
		return lab.Name, specDir, nil
	}

	// Positional topology name
	if len(args) > 0 && args[0] != "" {
		// Check if it matches a deployed lab by name
		labs, _ := newtlab.ListLabs()
		for _, l := range labs {
			if l == args[0] {
				state, loadErr := newtlab.LoadState(l)
				if loadErr != nil {
					return "", "", loadErr
				}
				return l, state.SpecDir, nil
			}
		}
		// Try as topology name
		d := resolveTopologyDir(args[0])
		if _, statErr := os.Stat(d); statErr != nil {
			return "", "", fmt.Errorf("topology %q not found: %s does not exist", args[0], d)
		}
		lab, labErr := newtlab.NewLab(d)
		if labErr != nil {
			return "", "", labErr
		}
		return lab.Name, d, nil
	}

	// Auto-detect from deployed labs
	labs, listErr := newtlab.ListLabs()
	if listErr != nil {
		return "", "", listErr
	}
	if len(labs) == 0 {
		return "", "", fmt.Errorf("no deployed labs; specify topology name or use -S <dir>")
	}
	if len(labs) == 1 {
		state, loadErr := newtlab.LoadState(labs[0])
		if loadErr != nil {
			return "", "", loadErr
		}
		return labs[0], state.SpecDir, nil
	}
	return "", "", fmt.Errorf("multiple labs deployed (%s); specify topology name", strings.Join(labs, ", "))
}

// resolveSpecDir resolves the spec directory from: -S flag > positional topology name > auto-detect.
func resolveSpecDir(args []string) (string, error) {
	_, dir, err := resolveTarget(args)
	return dir, err
}

// resolveLabName resolves a lab name from: -S flag > positional name > auto-detect.
func resolveLabName(args []string) (string, error) {
	name, _, err := resolveTarget(args)
	return name, err
}

// ============================================================================
// List Command
// ============================================================================

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List topologies and their deployment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			base := topologiesBaseDir()
			entries, err := os.ReadDir(base)
			if err != nil {
				return fmt.Errorf("cannot find topologies directory %s: %w", base, err)
			}

			// Get deployed labs for status
			deployedLabs := make(map[string]*newtlab.LabState)
			labs, _ := newtlab.ListLabs()
			for _, name := range labs {
				state, err := newtlab.LoadState(name)
				if err == nil {
					deployedLabs[name] = state
				}
			}

			t := cli.NewTable("TOPOLOGY", "DEVICES", "LINKS", "STATUS", "NODES").WithPrefix("  ")
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				topoDir := filepath.Join(base, e.Name(), "specs")
				devices, links := topoCounts(topoDir)
				if devices == 0 {
					continue
				}

				// Check deployment status
				status := "—"
				nodes := ""
				// Derive lab name the same way NewLab does
				lab, err := newtlab.NewLab(topoDir)
				if err == nil {
					if state, ok := deployedLabs[lab.Name]; ok {
						running, total := 0, 0
						for _, n := range state.Nodes {
							total++
							if n.Status == "running" {
								running++
							}
						}
						if running == total && total > 0 {
							status = green("deployed")
							nodes = fmt.Sprintf("%d/%d running", running, total)
						} else if running > 0 {
							status = yellow("degraded")
							nodes = fmt.Sprintf("%d/%d running", running, total)
						} else if total > 0 {
							status = yellow("stopped")
							nodes = fmt.Sprintf("0/%d", total)
						}
					}
				}

				t.Row(e.Name(), fmt.Sprintf("%d", devices), fmt.Sprintf("%d", links), status, nodes)
			}
			t.Flush()
			return nil
		},
	}
}

// topoCounts returns device and link counts from a topology.json.
func topoCounts(specDir string) (int, int) {
	path := filepath.Join(specDir, "topology.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	var topo struct {
		Devices map[string]json.RawMessage `json:"devices"`
		Links   []json.RawMessage          `json:"links"`
	}
	if err := json.Unmarshal(data, &topo); err != nil {
		return 0, 0
	}
	return len(topo.Devices), len(topo.Links)
}

// ============================================================================
// Misc
// ============================================================================

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

// humanBytes formats a byte count into a human-readable string.
func humanBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
