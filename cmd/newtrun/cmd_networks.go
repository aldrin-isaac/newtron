package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

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

func createNetwork(_ context.Context, name, description, _ string) error {
	c := newNewtronClient(name)
	// Server owns the path (§27, §33) — newt-server resolves
	// <networks-base>/<id> itself. The CLI passes only the description
	// seed. The networksBase parameter is retained for backward
	// CLI-flag compatibility but is no longer wire-relevant.
	info, err := c.CreateNetwork(description)
	if err != nil {
		return fmt.Errorf("create network %q: %w", name, err)
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
// ignored; for network-scoped calls (e.g. CreateNetwork), pass the
// target network's ID.
func newNewtronClient(networkID string) *newtronclient.Client {
	url := newtrunServerFlag
	if url == "" {
		url = os.Getenv("NEWTRUN_SERVER")
	}
	if url == "" {
		url = "http://127.0.0.1:18080"
	}
	// Identity + TLS come from the single owner of the newtron CLI client build
	// (§27) — one login serves every command across all three CLIs.
	c, err := newtronclient.NewCLIClient(url, networkID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	return c
}
