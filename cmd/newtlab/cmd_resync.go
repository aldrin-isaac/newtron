package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

func newResyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resync [network]",
		Short: "Re-establish link telemetry for a running lab",
		Long: `Resync a running lab's link telemetry without touching its VMs or data plane.

Ensures the lab has a telemetry token (minting one if it was deployed before the
token feature), injects it into the worker's bridge.json, and sends the running
newtlink a SIGHUP to hot-reload the credential. newtlink is NOT restarted — it
relays the QEMU socket connections between VMs, so restarting it would drop the
data plane. SIGHUP keeps those connections up and only rotates the push token,
so BridgeStats pushes authenticate against an --enforce-authorization server.

Use it after a newt-server restart (or upgrade) leaves a running lab's newtlink
pushing with no / a stale credential (symptom: 401s in the lab's bridge.log and
no link stats in 'newtlab status').

  newtlab resync 3node-vs-newtcon
  newtlab resync                    # auto-selects if only one lab`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			labName, err := resolveLabName(args)
			if err != nil {
				return err
			}
			fmt.Printf("Resyncing lab %s (SIGHUP token reload; VMs + data plane untouched)...\n", labName)
			if _, err := newtlab.ResyncBridges(labName); err != nil {
				return err
			}
			fmt.Printf("%s Lab %s resynced — newtlink reloaded its telemetry token\n", green("✓"), labName)
			return nil
		},
	}
	return cmd
}
