// NewtLab — VM orchestration for SONiC networks
//
// newtlab deploys QEMU virtual machines from newtron network specs.
// It reads topology.json, platforms.json, and nodes/*.json to create
// connected VMs with socket-based networking. No root, no bridges, no Docker.
//
// Usage:
//
//	newtlab list                     # show networks
//	newtlab deploy 2node-ngdp        # deploy VMs
//	newtlab status 2node-ngdp        # show VM status
//	newtlab ssh spine1               # SSH to a VM
//	newtlab destroy 2node-ngdp       # tear down
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/cli"
	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
	newtronclient "github.com/aldrin-isaac/newtron/pkg/newtron/client"
	"github.com/aldrin-isaac/newtron/pkg/newtron/settings"
	"github.com/aldrin-isaac/newtron/pkg/util"
	"github.com/aldrin-isaac/newtron/pkg/version"
)

var (
	dir           string
	verbose       bool
	newtronServer string
	newtlabServer string
	netID         string
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:               "newtlab",
	Short:             "VM orchestration for SONiC networks",
	SilenceUsage:      true,
	SilenceErrors:     true,
	CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	Long: `NewtLab deploys QEMU virtual machines from newtron network specs.

Networks are resolved by name from networks/.

  newtlab list                       # show networks
  newtlab deploy 2node-ngdp           # deploy VMs for the network
  newtlab status [network]          # show VM status
  newtlab ssh <node>                 # SSH to a VM
  newtlab console <node>             # serial console
  newtlab destroy [network]         # tear down
  newtlab provision [network]       # provision via newtron`,
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
	rootCmd.PersistentFlags().StringVar(&dir, "dir", "", "network directory (overrides network name)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().StringVar(&newtronServer, "newtron-server", httputil.DefaultServerURL, "newtron-server URL (newtlab consumes specs via /newtron/v1)")
	rootCmd.PersistentFlags().StringVar(&newtlabServer, "newtlab-server", "", "newtlab-server URL — the orchestrator URL newtlink pushes BridgeStats to (#118) and the read path for `newtlab status` link telemetry. Defaults to --newtron-server (same listener in the aggregated newt-server); set explicitly only for multi-host labs where remote workers reach the server at a different, publicly-reachable address. Env: NEWTLAB_SERVER")
	rootCmd.PersistentFlags().StringVar(&netID, "net-id", "", "newtron network ID (default: derived from the lab name, so concurrent labs get separate registration slots — issue #116)")

	rootCmd.AddCommand(
		newListCmd(),
		newDeployCmd(),
		newDestroyCmd(),
		newResyncCmd(),
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
// Network Resolution
// ============================================================================

// networksBaseDir returns the base directory for networks.
// Resolution: NEWTRUN_TOPOLOGIES env > settings > default.
func networksBaseDir() string {
	if v := os.Getenv("NEWTRUN_TOPOLOGIES"); v != "" {
		return v
	}
	if s, err := settings.Load(); err == nil && s.NetworksDir != "" {
		return s.NetworksDir
	}
	return "networks"
}

// resolveNetworkDir resolves a network name to its network directory.
// If name contains "/" it's used as-is. Otherwise resolved under networksBaseDir.
func resolveNetworkDir(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return filepath.Join(networksBaseDir(), name)
}

// networkNameFromPath derives the network name from a network directory
// path. Mirrors the convention used by newtron-server.s networkName():
// /path/to/<network>/specs → <network>; /path/to/<network> → <network>.
func networkNameFromPath(absDir string) string {
	base := filepath.Base(absDir)
	if base == "" && filepath.Base(filepath.Clean(dir)) == filepath.Base(dir) {
		return filepath.Base(filepath.Dir(absDir))
	}
	return base
}

// prepareLab returns a configured *newtlab.Lab for the given network
// reference. It constructs a newtron HTTP client, registers the spec
// directory with newtron (idempotent: 409 conflict is treated as
// success), and calls newtlab.NewLab which consumes specs via the
// newtron API (DESIGN_PRINCIPLES §27 — newtron owns spec files).
func prepareLab(ctx context.Context, args []string) (*newtlab.Lab, error) {
	name, dir, err := resolveTarget(args)
	if err != nil {
		return nil, err
	}
	// Default the network ID to the lab name so concurrent labs get
	// separate registration slots on newtron (issue #116). Operators can
	// still pin a different name via --net-id.
	effectiveNetID := netID
	if effectiveNetID == "" {
		effectiveNetID = name
	}
	// Identity (per-user session cache / NEWTRON_BEARER) and TLS posture
	// (NEWTRON_TLS_CERT/KEY/CA) both come from the single owner of the newtron
	// CLI client build (§27) — one `newtron auth login` carries through every
	// CLI invocation.
	client, err := newtronclient.NewCLIClient(newtronServer, effectiveNetID)
	if err != nil {
		return nil, err
	}
	// Ensure the network is registered on newtron-server so it can
	// serve this network's specs. The server resolves the spec directory
	// under its --networks-base; this client just names the network by
	// id (the topology is one facet of that network). RegisterNetwork is
	// idempotent — re-issuing is a no-op.
	if dir != "" {
		if _, regErr := client.CreateNetwork(""); regErr != nil {
			return nil, fmt.Errorf("registering network with newtron at %s: %w", newtronServer, regErr)
		}
	}
	lab, err := newtlab.NewLab(ctx, client, name)
	if err != nil {
		return nil, err
	}
	lab.OrchestratorURL = newtlabURL()
	return lab, nil
}

// newtlabURL resolves the URL newtlink pushes BridgeStats to (baked into
// the bridge config as orchestrator_url) and that `newtlab status` reads
// link telemetry from. Resolution order:
//
//	--newtlab-server flag > NEWTLAB_SERVER env > --newtron-server
//
// The final fallback is --newtron-server — the newt-server this CLI already
// talks to for specs — not an independent hard-coded default. In the
// aggregated newt-server model the spec API and the lab/orchestrator API are
// the same listener, so a single provided server URL must reach both: this
// keeps the baked orchestrator_url and the status read path on the server the
// operator actually pointed the CLI at, instead of silently defaulting to
// :18080 when only --newtron-server was given. Because deploy (which bakes the
// URL) and `newtlab status` (which reads it back) both resolve through this one
// helper, push target and read path are sourced identically by construction.
//
// The explicit --newtlab-server / NEWTLAB_SERVER override remains for
// multi-host labs, where remote workers must reach the server at a
// publicly-reachable address that differs from the operator's CLI-to-server URL.
func newtlabURL() string {
	url := newtlabServer
	if url == "" {
		url = os.Getenv("NEWTLAB_SERVER")
	}
	if url == "" {
		url = newtronServer
	}
	return url
}

// resolveTarget resolves both lab name and network directory from:
// -S flag > positional network name > auto-detect from deployed labs.
// This is the shared resolution logic used by resolveLabName and
// prepareLab. The network directory is no longer used for file reads
// (§27 — newtron owns spec files); it is the path newtron is asked
// to register and serve.
func resolveTarget(args []string) (labName string, dir string, err error) {
	// Explicit -S flag takes priority
	if dir != "" {
		absDir, absErr := filepath.Abs(dir)
		if absErr != nil {
			return "", "", fmt.Errorf("resolve network dir: %w", absErr)
		}
		return networkNameFromPath(absDir), absDir, nil
	}

	// Positional network name
	if len(args) > 0 && args[0] != "" {
		// Check if it matches a deployed lab by name
		labs, _ := newtlab.ListLabs()
		for _, l := range labs {
			if l == args[0] {
				state, loadErr := newtlab.LoadState(l)
				if loadErr != nil {
					return "", "", loadErr
				}
				// state.Dir is empty for older labs whose state.json
				// was written before Dir was persisted; fall back to
				// the canonical network dir for the network name so the
				// caller can still register the network with newtron.
				dir := state.Dir
				if dir == "" {
					dir = resolveNetworkDir(l)
				}
				return l, dir, nil
			}
		}
		// Try as network name
		d := resolveNetworkDir(args[0])
		if _, statErr := os.Stat(d); statErr != nil {
			return "", "", fmt.Errorf("network %q not found: %s does not exist", args[0], d)
		}
		return args[0], d, nil
	}

	// Auto-detect from deployed labs
	labs, listErr := newtlab.ListLabs()
	if listErr != nil {
		return "", "", listErr
	}
	if len(labs) == 0 {
		return "", "", fmt.Errorf("no deployed labs; specify network name or use -S <dir>")
	}
	if len(labs) == 1 {
		state, loadErr := newtlab.LoadState(labs[0])
		if loadErr != nil {
			return "", "", loadErr
		}
		return labs[0], state.Dir, nil
	}
	return "", "", fmt.Errorf("multiple labs deployed (%s); specify network name", strings.Join(labs, ", "))
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
		Short: "List networks and their deployment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			base := networksBaseDir()
			entries, err := os.ReadDir(base)
			if err != nil {
				return fmt.Errorf("cannot find networks directory %s: %w", base, err)
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

			// Display per-network rows. Device/link counts come from
			// the spec, which is newtron's data object (§27) — newtlab
			// does not read spec files. Operators who want per-node
			// detail run `newtlab status <network>`.
			t := cli.NewTable("NETWORK", "STATUS", "NODES").WithPrefix("  ")
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				// Skip directories without a specs subdirectory (not a network).
				if _, err := os.Stat(filepath.Join(base, e.Name(), "topology.json")); err != nil {
					continue
				}

				// Check deployment status against newtlab's own state.
				status := "—"
				nodes := ""
				if state, ok := deployedLabs[e.Name()]; ok {
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

				t.Row(e.Name(), status, nodes)
			}
			t.Flush()
			return nil
		},
	}
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
