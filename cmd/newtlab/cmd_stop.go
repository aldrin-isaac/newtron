package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <node>",
		Short: "Stop a VM (preserves disk)",
		Long: `Stop a running VM by killing its QEMU process.

The overlay disk is preserved, so the VM can be restarted with
'newtlab start' without losing state.

  newtlab stop leaf1
  newtlab start leaf1     # resume from saved disk`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeName := args[0]

			_, labName, err := findNodeState(nodeName)
			if err != nil {
				return err
			}

			if err := newtlab.StopByName(cmd.Context(), labName, nodeName); err != nil {
				return err
			}
			fmt.Printf("%s Stopped %s\n", green("✓"), nodeName)
			return nil
		},
	}
	return cmd
}

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <node>",
		Short: "Start a stopped VM",
		Long: `Start a previously stopped VM.

Launches QEMU using the existing overlay disk and waits for SSH
connectivity before returning.

  newtlab start leaf1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeName := args[0]

			_, labName, err := findNodeState(nodeName)
			if err != nil {
				return err
			}

			fmt.Printf("Starting %s...\n", nodeName)
			if err := newtlab.StartByName(cmd.Context(), labName, nodeName); err != nil {
				return err
			}
			fmt.Printf("%s Started %s\n", green("✓"), nodeName)
			return nil
		},
	}
	return cmd
}
