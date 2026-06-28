package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/cli"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Inspect the topology spec for the registered network",
	Long: `Inspect topology.json — the canonical structural definition of the
network (devices, links, metadata). Network-scoped; no device required.

For just the device-name list (lightweight), see 'network topology'.

Examples:
  newtron topology show
  newtron topology show --json`,
}

var topologyShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the full topology spec (devices, links, metadata)",
	Long: `Return the canonical 'spec.TopologySpecFile' substrate for the
registered network — every device with its nodeSpec, every link with its
endpoints, plus newtlab/metadata fields. This is the typed form newtron uses
internally; consumers needing only the names list should use
'network topology' for a lighter response.

Examples:
  newtron topology show
  newtron topology show --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		topo, err := app.client.GetTopology()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(topo)
		}
		printTopologySpec(topo)
		return nil
	},
}

// printTopologySpec renders a TopologySpecFile in human-readable form.
func printTopologySpec(t *spec.TopologySpecFile) {
	if t == nil {
		fmt.Println("(no topology loaded)")
		return
	}
	fmt.Printf("%s %s\n", bold("Version:"), t.Version)
	if t.Description != "" {
		fmt.Printf("%s %s\n", bold("Description:"), t.Description)
	}

	if len(t.Nodes) > 0 {
		fmt.Printf("\n%s\n", bold("Devices"))
		names := make([]string, 0, len(t.Nodes))
		for n := range t.Nodes {
			names = append(names, n)
		}
		sort.Strings(names)
		tb := cli.NewTable("DEVICE", "STEPS", "PORTS")
		for _, n := range names {
			d := t.Nodes[n]
			tb.Row(n, fmt.Sprintf("%d", len(d.Steps)), fmt.Sprintf("%d", len(d.Ports)))
		}
		tb.Flush()
	}

	if len(t.Links) > 0 {
		fmt.Printf("\n%s\n", bold("Links"))
		for _, link := range t.Links {
			fmt.Printf("  %s ↔ %s\n", link.A, link.Z)
		}
	}
}

func init() {
	topologyCmd.AddCommand(topologyShowCmd)
}
