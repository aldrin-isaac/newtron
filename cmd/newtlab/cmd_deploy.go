package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newDeployCmd() *cobra.Command {
	var host string
	var force bool
	var provision bool
	var parallel int

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy VMs from topology.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := requireSpecDir()
			if err != nil {
				return err
			}

			lab, err := newtlab.NewLab(dir)
			if err != nil {
				return err
			}
			lab.Force = force

			if host != "" {
				lab.FilterHost(host)
			}

			fmt.Println("Deploying VMs...")
			if err := lab.Deploy(); err != nil {
				return err
			}

			// Print summary
			state := lab.State
			fmt.Printf("\n%s Deployed %s (%d nodes)\n\n", green("✓"), lab.Name, len(state.Nodes))
			fmt.Printf("  %-16s %-10s %-12s %s\n", "NODE", "STATUS", "SSH PORT", "CONSOLE")
			fmt.Printf("  %-16s %-10s %-12s %s\n", "────────────────", "──────────", "────────────", "───────")
			for name, node := range state.Nodes {
				fmt.Printf("  %-16s %-10s %-12d %d\n", name, node.Status, node.SSHPort, node.ConsolePort)
			}

			if provision {
				fmt.Println("\nProvisioning devices...")
				if err := lab.Provision(parallel); err != nil {
					return err
				}
				fmt.Printf("%s Provisioning complete\n", green("✓"))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "deploy only nodes for this host")
	cmd.Flags().BoolVar(&force, "force", false, "force deploy (destroy existing first)")
	cmd.Flags().BoolVar(&provision, "provision", false, "provision devices after deploy")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "parallel provisioning threads")
	return cmd
}
