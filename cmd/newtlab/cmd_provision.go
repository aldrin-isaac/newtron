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
		Long: `Run topology reconcile on deployed VMs.

This invokes 'newtron <name> --topology intent reconcile -x' on each device
in the topology, replaying topology.json steps and delivering the resulting
CONFIG_DB projection to the device. Equivalent to the --provision flag on
'newtlab deploy'.

  newtlab provision 2node-ngdp
  newtlab provision 2node-ngdp --device leaf1    # single device
  newtlab provision 2node-ngdp --parallel 4      # parallel provisioning`,
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

			// If --device specified, set filter so Provision() only targets that device
			if device != "" {
				lab.DeviceFilter = []string{device}
			}

			fmt.Println("Provisioning devices...")
			if err := lab.Provision(cmd.Context(), parallel); err != nil {
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
