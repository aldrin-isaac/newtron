package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	newtronclient "github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// newTopologiesCmd lists newtron-registered networks. The CLI surface keeps
// the "topology" noun because it matches the operator vocabulary used in
// suite YAML and the newtrun mental model; the underlying call is to
// newtron, the single owner of spec data.
func newTopologiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "topologies",
		Short: "List topologies (newtron-registered networks)",
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
				fmt.Fprintf(w, "  %s\t%s\n", info.ID, info.SpecDir)
			}
			return w.Flush()
		},
	}
}

// newTopologyCmd is the singular form: create (and future delete) of
// topologies. Mirrors the `suite create` pattern so an operator who knows
// one knows the other.
func newTopologyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Create or manage topologies (newtron networks)",
	}
	cmd.AddCommand(newTopologyCreateCmd())
	return cmd
}

func newTopologyCreateCmd() *cobra.Command {
	var (
		description    string
		topologiesBase string
	)
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Scaffold a new topology and register it with newtron",
		Long: `Scaffold a new topology directory with zero-valued spec files
(topology.json, platforms.json, network.json) plus an empty profiles/
subdirectory, then register it as a newtron network in one call.

The spec_dir defaults to <topologies-base>/<name>/specs (resolved
against the current working directory). Override the base with
--topologies-base.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return createTopology(cmd.Context(), args[0], description, topologiesBase)
		},
	}
	c.Flags().StringVar(&description, "description", "", "free-text description seeded into topology.json")
	c.Flags().StringVar(&topologiesBase, "topologies-base", "newtrun/topologies", "base directory under which the topology dir is created")
	return c
}

func createTopology(_ context.Context, name, description, topologiesBase string) error {
	specDir, err := filepath.Abs(filepath.Join(topologiesBase, name, "specs"))
	if err != nil {
		return fmt.Errorf("resolve spec_dir: %w", err)
	}
	c := newNewtronClient(name)
	if err := c.ScaffoldNetwork(specDir, description); err != nil {
		return fmt.Errorf("scaffold network %q: %w", name, err)
	}
	fmt.Fprintf(os.Stderr, "created topology %s at %s\n", name, specDir)
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
	return newtronclient.New(url, networkID)
}
