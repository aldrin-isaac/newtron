package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newProvisionCmd() *cobra.Command {
	var device string
	var parallel int

	cmd := &cobra.Command{
		Use:   "provision [topology]",
		Short: "Provision devices via newtron",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveSpecDir(args)
			if err != nil {
				return err
			}

			lab, err := newtlab.NewLab(dir)
			if err != nil {
				return err
			}

			// If --device specified, filter to just that device
			if device != "" {
				state, loadErr := newtlab.LoadState(lab.Name)
				if loadErr != nil {
					return loadErr
				}
				if _, ok := state.Nodes[device]; !ok {
					return fmt.Errorf("device %q not found in lab %s", device, lab.Name)
				}
				// Filter state to single device for provisioning
				for name := range state.Nodes {
					if name != device {
						delete(state.Nodes, name)
					}
				}
			}

			fmt.Println("Provisioning devices...")
			if err := lab.Provision(parallel); err != nil {
				return err
			}
			fmt.Printf("%s Provisioning complete\n", green("✓"))
			return nil
		},
	}

	cmd.Flags().StringVar(&device, "device", "", "provision only this device")
	cmd.Flags().IntVar(&parallel, "parallel", 1, "parallel provisioning threads")
	return cmd
}
