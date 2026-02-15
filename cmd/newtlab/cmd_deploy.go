package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newDeployCmd() *cobra.Command {
	var host string
	var force bool
	var provision bool
	var parallel int

	cmd := &cobra.Command{
		Use:   "deploy [topology]",
		Short: "Deploy VMs from topology",
		Long: `Deploy VMs from a topology spec. The topology can be a name
(resolved under newtest/topologies/) or specified via -S.

  newtlab deploy 2node
  newtlab deploy 2node --provision`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveSpecDir(args)
			if err != nil {
				return err
			}

			lab, err := newtlab.NewLab(dir)
			if err != nil {
				return err
			}
			lab.Force = force
			lab.OnProgress = func(phase, detail string) {
				fmt.Printf("  [%s] %s\n", phase, detail)
			}

			if host != "" {
				lab.FilterHost(host)
			}

			fmt.Println("Deploying VMs...")
			if err := lab.Deploy(cmd.Context()); err != nil {
				return err
			}

			// Print summary
			state := lab.State
			fmt.Printf("\n%s Deployed %s (%d nodes)\n\n", green("✓"), lab.Name, len(state.Nodes))
			t := cli.NewTable("NODE", "STATUS", "SSH PORT", "CONSOLE").WithPrefix("  ")
			nodeNames := make([]string, 0, len(state.Nodes))
			for name := range state.Nodes {
				nodeNames = append(nodeNames, name)
			}
			sort.Strings(nodeNames)
			for _, name := range nodeNames {
				node := state.Nodes[name]
				t.Row(name, node.Status, fmt.Sprintf("%d", node.SSHPort), fmt.Sprintf("%d", node.ConsolePort))
			}
			t.Flush()

			if provision {
				fmt.Println("\nProvisioning devices...")
				if err := lab.Provision(cmd.Context(), parallel); err != nil {
					return err
				}
				fmt.Printf("%s Provisioning complete\n", green("✓"))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "deploy only nodes assigned to this host (multi-host mode)")
	cmd.Flags().BoolVar(&force, "force", false, "force deploy (destroy existing first)")
	cmd.Flags().BoolVar(&provision, "provision", false, "provision devices after deploy")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "parallel provisioning threads")
	return cmd
}
