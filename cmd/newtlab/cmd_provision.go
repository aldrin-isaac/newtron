package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newProvisionCmd() *cobra.Command {
	var device string
	var parallel int

	cmd := &cobra.Command{
		Use:   "provision [network]",
		Short: "Provision devices via newtron",
		Long: `Reconcile a deployed network to its topology.

For each device, newtlab calls newtron's reconcile over its HTTP API (the single
owner of "reconcile a device", §27) — replaying topology.json steps and
delivering the resulting CONFIG_DB projection to the device. Equivalent to the
--provision flag on 'newtlab deploy'.

  newtlab provision 2node-ngdp
  newtlab provision 2node-ngdp --device leaf1    # single device
  newtlab provision 2node-ngdp --parallel 4      # parallel provisioning`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := prepareLab(cmd.Context(), args)
			if err != nil {
				return err
			}

			// If --device specified, set filter so Provision() only targets that device
			if device != "" {
				lab.DeviceFilter = []string{device}
			}

			// Stream per-device progress to stdout — the same OnProgress
			// mechanism the API server wires to the SSE broker (#373).
			lab.OnProgress = func(phase, detail string) {
				fmt.Printf("  [%s] %s\n", phase, detail)
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
