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
