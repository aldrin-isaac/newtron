package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron/api"
)

var networkCmd = &cobra.Command{
	Use:   "network",
	Short: "Server network management",
	Long: `Manage registered networks on the newtron server.

Networks are registered automatically when the CLI first connects to the server.
These commands let you inspect, reload, or unregister networks from the server
without restarting it.

Examples:
  newtron network list
  newtron network reload
  newtron network unregister`,
}

var networkListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered networks",
	Long: `List all networks currently registered with the newtron server.

Shows the network ID, spec directory, whether a topology is defined, and
the number of connected nodes.

Examples:
  newtron network list
  newtron network list --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		networks, err := app.client.ListNetworks()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(networks)
		}

		if len(networks) == 0 {
			fmt.Println("No networks registered")
			return nil
		}

		t := cli.NewTable("ID", "SPEC DIR", "TOPOLOGY", "NODES")
		for _, n := range networks {
			topology := "no"
			if n.HasTopology {
				topology = "yes"
			}
			t.Row(n.ID, dash(n.SpecDir), topology, fmt.Sprintf("%d", len(n.Nodes)))
		}
		t.Flush()

		return nil
	},
}

var networkUnregisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Unregister the current network from the server",
	Long: `Unregister the current network from the newtron server.

This stops all node actors for the network and removes it from the server's
registry. The spec files on disk are not affected. The network will be
re-registered automatically on the next CLI command.

Examples:
  newtron network unregister`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := app.client.UnregisterNetwork(); err != nil {
			return err
		}
		fmt.Printf("Network %q unregistered.\n", app.networkID)
		return nil
	},
}

var networkReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload network specs from disk",
	Long: `Reload the current network's specs from disk without restarting the server.

Use this after editing network.json, service specs, or other spec files to make
the server pick up the changes without a restart.

Examples:
  newtron network reload`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := app.client.ReloadNetwork(); err != nil {
			return err
		}
		fmt.Printf("Network %q specs reloaded.\n", app.networkID)
		return nil
	},
}

var networkTopologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "List topology device names",
	Long: `List the device names defined in the topology for the current network.

These are the devices declared in topology.json, regardless of whether
they are currently connected.

Examples:
  newtron network topology
  newtron network topology --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := app.client.TopologyDeviceNames()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(names)
		}

		if len(names) == 0 {
			fmt.Println("No devices in topology")
			return nil
		}

		for _, name := range names {
			fmt.Println(name)
		}
		return nil
	},
}

var networkHostCmd = &cobra.Command{
	Use:   "host <name>",
	Short: "Show host device profile",
	Long: `Show the SSH connection profile for a host device (non-SONiC VM).

Host devices are virtual machines defined in the topology that are not
SONiC switches (e.g., traffic generators, test endpoints).

Examples:
  newtron network host host1
  newtron network host host1 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profile, err := app.client.GetHostProfile(args[0])
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(profile)
		}

		fmt.Printf("Host: %s\n", bold(args[0]))
		fmt.Printf("Management IP: %s\n", profile.MgmtIP)
		fmt.Printf("SSH User: %s\n", profile.SSHUser)
		fmt.Printf("SSH Port: %d\n", profile.SSHPort)
		return nil
	},
}

// networkInfoFields references api.NetworkInfo to satisfy the import.
// ListNetworks returns []api.NetworkInfo; fields are accessed by name in networkListCmd.
var _ api.NetworkInfo

func init() {
	networkCmd.AddCommand(networkListCmd)
	networkCmd.AddCommand(networkUnregisterCmd)
	networkCmd.AddCommand(networkReloadCmd)
	networkCmd.AddCommand(networkTopologyCmd)
	networkCmd.AddCommand(networkHostCmd)
}
