package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

// newTopologiesCmd is the plural form: list-only, matches the existing
// `suites` / `actions` listing commands. Authoring lives under the
// singular `topology` command below.
func newTopologiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "topologies",
		Short: "List available topologies known to newtrun-server",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			names, err := c.ListTopologies(ctx)
			if err != nil {
				return err
			}
			fmt.Println("newtrun topologies")
			fmt.Println()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  TOPOLOGY")
			for _, name := range names {
				fmt.Fprintf(w, "  %s\n", name)
			}
			return w.Flush()
		},
	}
}

// newTopologyCmd is the singular form: create (and future delete) of
// topology directories. Mirrors the `suite create` pattern so an
// operator who knows one knows the other.
func newTopologyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Create or manage topology directories via newtrun-server",
	}
	cmd.AddCommand(newTopologyCreateCmd())
	return cmd
}

func newTopologyCreateCmd() *cobra.Command {
	var description string
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Bootstrap a new topology directory on the server",
		Long: `Bootstrap a new topology directory with zero-valued spec files
(topology.json, platforms.json, network.json) and an empty profiles/ subdirectory.
The returned spec_dir is the value to pass to newtron's POST /newtron/v1/network
when registering this topology as a network.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return createTopology(cmd.Context(), args[0], description)
		},
	}
	c.Flags().StringVar(&description, "description", "", "free-text description seeded into topology.json")
	return c
}

func createTopology(ctx context.Context, name, description string) error {
	c := newClient()
	if err := requireServer(ctx, c); err != nil {
		return err
	}
	resp, err := c.CreateTopology(ctx, api.CreateTopologyRequest{
		Name:        name,
		Description: description,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created topology %s at %s\n", resp.Name, resp.SpecDir)
	return nil
}
