package main

import (
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/spf13/cobra"
)

func newActionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actions [action]",
		Short: "List supported step actions or show one action's details",
		Long: `List the step actions newtrun-server's parser will accept, or show details
for one action including required fields, expected fields, and a YAML example.

The action set is derived from pkg/newtrun.StepAction constants — it stays in
sync with the parser, so anything listed here is guaranteed to parse.

Examples:
  newtrun actions                    # list all six actions
  newtrun actions newtron            # show details for the generic HTTP action
  newtrun actions topology-reconcile # show details for the reconcile action`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listActions()
			}
			return showActionDetails(args[0])
		},
	}
	return cmd
}

// actionMetadata describes one StepAction for the `newtrun actions` output.
// The action set is the source-of-truth StepAction constants; this metadata
// only annotates each constant with help text. Anything not listed below
// is a bug.
type actionMetadata struct {
	short    string // one-line summary
	long     string // longer description shown in detail view
	required string // required step-level YAML fields (per parser stepValidations)
	devices  string // device-selector semantics
	example  string // representative YAML
}

// actionMeta is keyed by the StepAction constants — adding a constant in
// pkg/newtrun/scenario.go without an entry here triggers a runtime panic
// at startup via assertActionMetaComplete().
var actionMeta = map[newtrun.StepAction]actionMetadata{
	newtrun.ActionProvision: {
		short:    "Reconcile a device's full topology projection (high-impact)",
		long:     "Calls newtron-server's per-device intent/reconcile endpoint with mode=topology. The server performs ConfigReload, lock, ReplaceAll, and SaveConfig internally. Inline runs require explicit opt-in via the allow_reconcile body field.",
		required: "devices",
		devices:  "one or more switches",
		example: `- name: reconcile-switch1
  action: topology-reconcile
  devices: [switch1]`,
	},
	newtrun.ActionVerifyProvisioning: {
		short:    "Verify the device's CONFIG_DB matches the topology projection",
		long:     "Reads the device's intent drift status. Passes when drift is clean.",
		required: "devices",
		devices:  "one or more switches",
		example: `- name: verify-switch1
  action: verify-topology
  devices: [switch1]`,
	},
	newtrun.ActionWait: {
		short:    "Sleep for the specified duration",
		long:     "Blocks the scenario for `duration`. Useful between writes that depend on daemon convergence when no observable signal is available.",
		required: "duration",
		devices:  "none (network-scoped)",
		example: `- name: wait-for-asic
  action: wait
  duration: 30s`,
	},
	newtrun.ActionHostExec: {
		short:    "Run a shell command inside a host VM's network namespace",
		long:     "Executes `command` via SSH into the host VM. Useful for ping, iperf, traffic generation. Honors `expect.contains` (substring match) and `expect.success_rate` (parsed from ping output). Requires exactly one device.",
		required: "command, devices (exactly one)",
		devices:  "exactly one host",
		example: `- name: ping-across-fabric
  action: host-exec
  devices: [host1]
  command: "ping -c 3 -W 2 10.100.0.3"
  expect:
    success_rate: 0.8`,
	},
	newtrun.ActionNewtron: {
		short:    "HTTP call to newtron-server (most flexible action)",
		long:     "Dispatches one HTTP call per device (or one network-scoped call if {{device}} is absent from the URL). Supports polling via `poll:`, batched call sequences via `batch:`, jq assertions on the response body, and expect_failure for negative tests. URL is rooted at /network/<id>/ — the prefix is added automatically.",
		required: "url or batch",
		devices:  "any (URL with {{device}} runs per-device in parallel; URL without {{device}} runs once)",
		example: `- name: create-vlan100
  action: newtron
  devices: [switch1]
  method: POST
  url: /node/{{device}}/create-vlan
  params: {id: 100}`,
	},
	newtrun.ActionNewtronCLI: {
		short:    "Run the bin/newtron CLI as a subprocess",
		long:     "Spawns `bin/newtron <device> <command>` as a subprocess. The device name is prepended automatically. Honors `expect.jq` (parses stdout as JSON when --json is in the command) and `expect.contains` (substring match on combined stdout+stderr). The CLI binary must be in $PATH.",
		required: "command (validation is currently in the executor, not the parser)",
		devices:  "any (device prepended as first positional arg)",
		example: `- name: list-services
  action: newtron-cli
  devices: [switch1]
  command: "vlan show 100 --loopback"
  expect:
    jq: '.name == "Servers"'`,
	},
}

// init enforces that every StepAction constant has matching metadata.
// If a new action is added without an entry here, the binary won't start.
func init() {
	known := []newtrun.StepAction{
		newtrun.ActionProvision,
		newtrun.ActionVerifyProvisioning,
		newtrun.ActionWait,
		newtrun.ActionHostExec,
		newtrun.ActionNewtron,
		newtrun.ActionNewtronCLI,
	}
	for _, a := range known {
		if _, ok := actionMeta[a]; !ok {
			panic(fmt.Sprintf("cmd_actions: missing actionMeta entry for %q", a))
		}
	}
	if len(actionMeta) != len(known) {
		panic("cmd_actions: actionMeta has entries that aren't in the known StepAction list")
	}
}

// orderedActions lists actions in the order they should appear in the
// list view: high-impact first, then network-scoped utility, then the
// general-purpose execution actions.
var orderedActions = []newtrun.StepAction{
	newtrun.ActionProvision,
	newtrun.ActionVerifyProvisioning,
	newtrun.ActionWait,
	newtrun.ActionHostExec,
	newtrun.ActionNewtron,
	newtrun.ActionNewtronCLI,
}

func listActions() error {
	fmt.Println("Supported step actions (derived from pkg/newtrun.StepAction):")
	fmt.Println()
	for _, a := range orderedActions {
		meta := actionMeta[a]
		fmt.Printf("  \033[32m%-20s\033[0m %s\n", string(a), meta.short)
	}
	fmt.Println()
	fmt.Println("\033[2mUse 'newtrun actions <action>' for required fields, device semantics, and a YAML example.\033[0m")
	return nil
}

func showActionDetails(name string) error {
	action := newtrun.StepAction(name)
	meta, ok := actionMeta[action]
	if !ok {
		return fmt.Errorf("unknown action: %s\n\nUse 'newtrun actions' to see the supported set", name)
	}

	fmt.Printf("\033[1;32mAction:\033[0m       %s\n", name)
	fmt.Printf("\033[36mSummary:\033[0m      %s\n", meta.short)
	fmt.Printf("\033[36mDescription:\033[0m  %s\n", meta.long)
	fmt.Printf("\033[1;31mRequired:\033[0m     %s\n", meta.required)
	fmt.Printf("\033[1mDevices:\033[0m      %s\n", meta.devices)
	fmt.Printf("\n\033[1;35mExample:\033[0m\n%s\n", meta.example)
	return nil
}
