package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	newtronclient "github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// newNetworksCmd lists newtron-registered networks. The underlying call
// is to newtron, the single owner of network specs.
func newNetworksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "networks",
		Short: "List newtron-registered networks",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newNewtronClient("")
			infos, err := c.ListNetworks()
			if err != nil {
				return fmt.Errorf("list networks: %w", err)
			}
			fmt.Println("newtron-registered networks")
			fmt.Println()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  ID\tSPEC_DIR")
			for _, info := range infos {
				fmt.Fprintf(w, "  %s\t%s\n", info.ID, info.Dir)
			}
			return w.Flush()
		},
	}
}

// newNetworkCmd is the singular form: create (and future delete) of
// networks. Mirrors the `suite create` pattern so an operator who knows
// one knows the other.
func newNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Create or manage networks",
	}
	cmd.AddCommand(newNetworkCreateCmd())
	return cmd
}

func newNetworkCreateCmd() *cobra.Command {
	var (
		description  string
		networksBase string
	)
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new network and register it with newtron",
		Long: `Scaffold a new network directory with zero-valued spec files
(network.json, topology.json, platforms.json) plus an empty profiles/
subdirectory, then register it as a newtron network in one call.

The dir defaults to <networks-base>/<name>/specs (resolved
against the current working directory). Override the base with
--networks-base.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return createNetwork(cmd.Context(), args[0], description, networksBase)
		},
	}
	c.Flags().StringVar(&description, "description", "", "free-text description seeded into topology.json")
	c.Flags().StringVar(&networksBase, "networks-base", "networks", "base directory under which the network dir is created")
	return c
}

func createNetwork(_ context.Context, name, description, networksBase string) error {
	dir, err := filepath.Abs(filepath.Join(networksBase, name))
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}
	c := newNewtronClient(name)
	// CLI workflow: the caller picks the path explicitly so the
	// scaffold lands inside the networks/ convention. The
	// server-derived mode (#122) is for UI clients that don't track
	// newtron's on-disk layout — newtrun's CLI does.
	info, err := c.ScaffoldNetwork(dir, description)
	if err != nil {
		return fmt.Errorf("scaffold network %q: %w", name, err)
	}
	// Print the server-resolved dir rather than the value the
	// CLI passed in. They are the same here, but reading it from the
	// response keeps the "server owns the layout" contract honest —
	// any future evolution where the server normalizes paths (resolves
	// symlinks, expands env vars) doesn't drift the printed value.
	fmt.Fprintf(os.Stderr, "created network %s at %s\n", name, info.Dir)
	return nil
}

// newNewtronClient builds a newtron client pointed at the same base URL
// the rest of the CLI uses for newtrun (newt-server runs both engines on
// one port; --newtrun-server already names "newt-server or newtrun-server
// directly"). For server-level calls like ListNetworks, networkID is
// ignored; for network-scoped calls (e.g. ScaffoldNetwork), pass the
// target network's ID.
func newNewtronClient(networkID string) *newtronclient.Client {
	url := newtrunServerFlag
	if url == "" {
		url = os.Getenv("NEWTRUN_SERVER")
	}
	if url == "" {
		url = "http://127.0.0.1:18080"
	}
	// Honor the per-user session cache the same way the newtron
	// CLI does — one login serves every command across all three
	// CLIs. LoadCLISession resolves --user / NEWTRON_USER against
	// the multi-user cache and returns nil on missing / expired /
	// ambiguous cache; WithBearer("") is a no-op.
	var bearerKey string
	if rec, err := newtronclient.LoadCLISession(os.Getenv("NEWTRON_USER"), url); err == nil && rec != nil {
		bearerKey = rec.Key
	}
	tlsCfg, err := httputil.LoadClientTLSConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading client TLS config from env: %v\n", err)
		os.Exit(1)
	}
	return newtronclient.New(url, networkID, newtronclient.WithBearer(bearerKey), newtronclient.WithTLS(tlsCfg))
}
