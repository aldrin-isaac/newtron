package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/cli"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

var (
	nodeCreateMgmtIP      string
	nodeCreateLoopbackIP  string
	nodeCreateZone        string
	nodeCreatePlatform    string
	nodeCreateUnderlayASN int
	nodeCreateSSHUser     string
	nodeCreateSSHPass     string
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Manage nodes",
	Long: `Manage nodes.

A node defines per-node settings including management IP, loopback IP,
zone membership, platform, and EVPN peering configuration.

Examples:
  newtron node list
  newtron node show switch1
  newtron node create switch3 --mgmt-ip 10.0.0.3 --loopback-ip 10.0.0.3 --zone dc1 -x
  newtron node delete switch3 -x`,
}

var nodeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all nodes",
	RunE: func(cmd *cobra.Command, args []string) error {
		nodes, err := app.client.ListNodeSpecs()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(nodes)
		}

		if len(nodes) == 0 {
			fmt.Println("No nodes defined")
			return nil
		}

		t := cli.NewTable("NAME", "MGMT IP", "LOOPBACK", "ZONE", "PLATFORM", "ASN")
		for _, name := range nodes {
			p, _ := app.client.ShowNodeSpec(name)
			if p != nil {
				asn := ""
				if p.UnderlayASN > 0 {
					asn = fmt.Sprintf("%d", p.UnderlayASN)
				}
				t.Row(name, p.MgmtIP, p.LoopbackIP, p.Zone, p.Platform, asn)
			}
		}
		t.Flush()

		return nil
	},
}

var nodeShowCmd = &cobra.Command{
	Use:   "show <node-name>",
	Short: "Show node details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		p, err := app.client.ShowNodeSpec(name)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(p)
		}

		fmt.Printf("Node: %s\n", bold(name))
		fmt.Printf("Management IP: %s\n", p.MgmtIP)
		fmt.Printf("Loopback IP: %s\n", p.LoopbackIP)
		fmt.Printf("Zone: %s\n", p.Zone)
		if p.Platform != "" {
			fmt.Printf("Platform: %s\n", p.Platform)
		}
		if p.UnderlayASN > 0 {
			fmt.Printf("Underlay ASN: %d\n", p.UnderlayASN)
		}
		if p.MAC != "" {
			fmt.Printf("MAC: %s\n", p.MAC)
		}
		if p.SSHUser != "" {
			fmt.Printf("SSH User: %s\n", p.SSHUser)
		}
		if p.EVPN != nil {
			fmt.Println("\nEVPN Peering:")
			if len(p.EVPN.Peers) > 0 {
				fmt.Printf("  Peers: %v\n", p.EVPN.Peers)
			}
			if p.EVPN.RouteReflector {
				fmt.Println("  Route Reflector: yes")
			}
			if p.EVPN.ClusterID != "" {
				fmt.Printf("  Cluster ID: %s\n", p.EVPN.ClusterID)
			}
		}

		return nil
	},
}

var nodeCreateCmd = &cobra.Command{
	Use:   "create <node-name>",
	Short: "Create a new node",
	Long: `Create a new node.

This is a spec-level command (no device needed).

Flags:
  --mgmt-ip       Management IP address (required)
  --loopback-ip   Loopback IP address
  --zone          Zone name (required)
  --platform      Platform name
  --underlay-asn  BGP underlay AS number
  --ssh-user      SSH username
  --ssh-pass      SSH password
  --ssh-port      SSH port (default 22)

Examples:
  newtron node create switch3 --mgmt-ip 10.0.0.3 --loopback-ip 10.0.0.3 --zone dc1 --platform ciscovs -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if nodeCreateMgmtIP == "" {
			return fmt.Errorf("--mgmt-ip is required")
		}
		if nodeCreateZone == "" {
			return fmt.Errorf("--zone is required")
		}

		if err := app.client.CreateNodeSpec(newtron.CreateNodeSpecRequest{
			Name:        name,
			MgmtIP:      nodeCreateMgmtIP,
			LoopbackIP:  nodeCreateLoopbackIP,
			Zone:        nodeCreateZone,
			Platform:    nodeCreatePlatform,
			UnderlayASN: nodeCreateUnderlayASN,
			SSHUser:     nodeCreateSSHUser,
			SSHPass:     nodeCreateSSHPass,
		}, execOpts()); err != nil {
			return err
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Created node '%s'\n", name)
		return nil
	},
}

var nodeDeleteCmd = &cobra.Command{
	Use:   "delete <node-name>",
	Short: "Delete a node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if err := app.client.DeleteNodeSpec(name, execOpts(), false); err != nil {
			return err
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Deleted node '%s'\n", name)
		return nil
	},
}

func init() {
	nodeCreateCmd.Flags().StringVar(&nodeCreateMgmtIP, "mgmt-ip", "", "Management IP address (required)")
	nodeCreateCmd.Flags().StringVar(&nodeCreateLoopbackIP, "loopback-ip", "", "Loopback IP address")
	nodeCreateCmd.Flags().StringVar(&nodeCreateZone, "zone", "", "Zone name (required)")
	nodeCreateCmd.Flags().StringVar(&nodeCreatePlatform, "platform", "", "Platform name")
	nodeCreateCmd.Flags().IntVar(&nodeCreateUnderlayASN, "underlay-asn", 0, "BGP underlay AS number")
	nodeCreateCmd.Flags().StringVar(&nodeCreateSSHUser, "ssh-user", "", "SSH username")
	nodeCreateCmd.Flags().StringVar(&nodeCreateSSHPass, "ssh-pass", "", "SSH password")

	nodeCmd.AddCommand(nodeListCmd)
	nodeCmd.AddCommand(nodeShowCmd)
	nodeCmd.AddCommand(nodeCreateCmd)
	nodeCmd.AddCommand(nodeDeleteCmd)
}
