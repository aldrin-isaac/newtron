package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newDeployCmd() *cobra.Command {
	var host string
	var force bool
	var provision bool
	var parallel int
	var monitor bool

	cmd := &cobra.Command{
		Use:   "deploy [topology]",
		Short: "Deploy VMs from topology",
		Long: `Deploy VMs from a topology spec. The topology can be a name
(resolved under newtrun/topologies/) or specified via -S.

  newtlab deploy 2node-ngdp
  newtlab deploy 2node-ngdp --monitor
  newtlab deploy 2node-ngdp --provision`,
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

			if host != "" {
				lab.FilterHost(host)
			}

			if monitor {
				return deployWithMonitor(cmd, lab, provision, parallel)
			}

			lab.OnProgress = func(phase, detail string) {
				fmt.Printf("  [%s] %s\n", phase, detail)
			}

			fmt.Println("Deploying VMs...")
			if err := lab.Deploy(cmd.Context()); err != nil {
				return err
			}

			printDeploySummary(lab)

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
	cmd.Flags().BoolVarP(&monitor, "monitor", "m", false, "show live status during deploy")
	return cmd
}

// deployWithMonitor runs deploy in a goroutine and shows the status monitor.
func deployWithMonitor(cmd *cobra.Command, lab *newtlab.Lab, provision bool, parallel int) error {
	var deployErr error
	deployDone := make(chan struct{})
	go func() {
		deployErr = lab.Deploy(cmd.Context())
		close(deployDone)
	}()

	// Wait for state file to be created by the deploy goroutine.
	time.Sleep(2 * time.Second)

	// Monitor until deploy phases clear or deploy goroutine finishes.
	_ = monitorLab(lab.Name, deployDone)

	// Wait for deploy goroutine to finish (may already be done).
	<-deployDone
	if deployErr != nil {
		return deployErr
	}

	fmt.Printf("\n%s Deploy complete\n", green("✓"))

	if provision {
		fmt.Println("\nProvisioning devices...")
		if err := lab.Provision(cmd.Context(), parallel); err != nil {
			return err
		}
		fmt.Printf("%s Provisioning complete\n", green("✓"))
	}

	return nil
}

func printDeploySummary(lab *newtlab.Lab) {
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
}
