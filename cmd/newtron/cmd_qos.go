package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/spec"
)

var qosCmd = &cobra.Command{
	Use:   "qos",
	Short: "Manage QoS policies",
	Long: `Manage QoS policies (spec authoring and device application).

Spec-level commands (no device needed):
  list, show, create, delete, add-queue, remove-queue

Device-level commands (requires -d flag):
  apply, remove

Examples:
  newtron qos list
  newtron qos show 8q-datacenter
  newtron qos create my-policy --description "Custom 4-queue policy"
  newtron qos add-queue my-policy 0 --type dwrr --weight 10 --dscp 0,1,2 --name best-effort
  newtron qos add-queue my-policy 1 --type strict --dscp 46,48 --name realtime
  newtron qos remove-queue my-policy 1
  newtron qos delete my-policy
  newtron -d leaf1-ny qos apply Ethernet0 8q-datacenter -x
  newtron -d leaf1-ny qos remove Ethernet0 -x`,
}

var qosListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all QoS policies",
	RunE: func(cmd *cobra.Command, args []string) error {
		policies := app.net.ListQoSPolicies()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(policies)
		}

		if len(policies) == 0 {
			fmt.Println("No QoS policies defined")
			return nil
		}

		sort.Strings(policies)

		t := cli.NewTable("NAME", "QUEUES", "DESCRIPTION")

		for _, name := range policies {
			policy, err := app.net.GetQoSPolicy(name)
			if err != nil {
				continue
			}
			t.Row(name, fmt.Sprintf("%d", len(policy.Queues)), policy.Description)
		}
		t.Flush()

		return nil
	},
}

var qosShowCmd = &cobra.Command{
	Use:   "show <policy-name>",
	Short: "Show QoS policy details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		policyName := args[0]

		policy, err := app.net.GetQoSPolicy(policyName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(policy)
		}

		fmt.Printf("QoS Policy: %s\n", bold(policyName))
		if policy.Description != "" {
			fmt.Printf("Description: %s\n", policy.Description)
		}
		fmt.Printf("Queues: %d\n", len(policy.Queues))
		fmt.Println()

		if len(policy.Queues) == 0 {
			fmt.Println("  (no queues configured)")
			return nil
		}

		t := cli.NewTable("INDEX", "NAME", "TYPE", "WEIGHT", "ECN", "DSCP").WithPrefix("  ")

		for i, q := range policy.Queues {
			weight := dashInt(q.Weight)
			ecn := "-"
			if q.ECN {
				ecn = "yes"
			}
			dscp := "-"
			if len(q.DSCP) > 0 {
				parts := make([]string, len(q.DSCP))
				for j, d := range q.DSCP {
					parts[j] = fmt.Sprintf("%d", d)
				}
				dscp = strings.Join(parts, ",")
			}
			name := dash(q.Name)
			t.Row(fmt.Sprintf("%d", i), name, q.Type, weight, ecn, dscp)
		}
		t.Flush()

		return nil
	},
}

var qosCreateDescription string

var qosCreateCmd = &cobra.Command{
	Use:   "create <policy-name>",
	Short: "Create a new QoS policy",
	Long: `Create a new QoS policy in network.json.

This creates an empty policy. Use 'qos add-queue' to add queues.

Examples:
  newtron qos create my-policy --description "Custom 4-queue policy"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		policyName := args[0]

		// Check if already exists
		if _, err := app.net.GetQoSPolicy(policyName); err == nil {
			return fmt.Errorf("QoS policy '%s' already exists", policyName)
		}

		authCtx := auth.NewContext().WithResource(policyName)
		if err := checkExecutePermission(auth.PermQoSCreate, authCtx); err != nil {
			return err
		}

		policy := &spec.QoSPolicy{
			Description: qosCreateDescription,
			Queues:      []*spec.QoSQueue{},
		}

		fmt.Printf("QoS Policy: %s\n", policyName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.SaveQoSPolicy(policyName, policy); err != nil {
			return fmt.Errorf("saving QoS policy: %w", err)
		}

		fmt.Printf("Created QoS policy '%s'\n", policyName)
		return nil
	},
}

var qosDeleteCmd = &cobra.Command{
	Use:   "delete <policy-name>",
	Short: "Delete a QoS policy",
	Long: `Delete a QoS policy from network.json.

Fails if any service references the policy.

Examples:
  newtron qos delete my-policy`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		policyName := args[0]

		// Verify it exists
		if _, err := app.net.GetQoSPolicy(policyName); err != nil {
			return err
		}

		authCtx := auth.NewContext().WithResource(policyName)
		if err := checkExecutePermission(auth.PermQoSDelete, authCtx); err != nil {
			return err
		}

		fmt.Printf("Deleting QoS policy: %s\n", policyName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		if err := app.net.DeleteQoSPolicy(policyName); err != nil {
			return err
		}

		fmt.Printf("Deleted QoS policy '%s'\n", policyName)
		return nil
	},
}

var (
	addQueueType   string
	addQueueWeight int
	addQueueDSCP   string
	addQueueName   string
	addQueueECN    bool
)

var qosAddQueueCmd = &cobra.Command{
	Use:   "add-queue <policy-name> <queue-id>",
	Short: "Add a queue to a QoS policy",
	Long: `Add a queue to a QoS policy in network.json.

The queue-id is the queue index (0-7). The --type flag is required.

Examples:
  newtron qos add-queue my-policy 0 --type dwrr --weight 10 --dscp 0,1,2 --name best-effort
  newtron qos add-queue my-policy 7 --type strict --dscp 46,48 --name realtime --ecn`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		policyName := args[0]

		var queueID int
		if _, err := fmt.Sscanf(args[1], "%d", &queueID); err != nil {
			return fmt.Errorf("invalid queue-id: %s", args[1])
		}
		if queueID < 0 || queueID > 7 {
			return fmt.Errorf("queue-id must be 0-7, got %d", queueID)
		}

		if addQueueType == "" {
			return fmt.Errorf("--type is required (dwrr, strict)")
		}
		if addQueueType != "dwrr" && addQueueType != "strict" {
			return fmt.Errorf("--type must be 'dwrr' or 'strict', got '%s'", addQueueType)
		}
		if addQueueType == "dwrr" && addQueueWeight <= 0 {
			return fmt.Errorf("--weight is required for dwrr queues")
		}

		authCtx := auth.NewContext().WithResource(policyName)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		policy, err := app.net.GetQoSPolicy(policyName)
		if err != nil {
			return err
		}

		// Parse DSCP values
		var dscpValues []int
		if addQueueDSCP != "" {
			for _, part := range strings.Split(addQueueDSCP, ",") {
				part = strings.TrimSpace(part)
				var val int
				if _, err := fmt.Sscanf(part, "%d", &val); err != nil {
					return fmt.Errorf("invalid DSCP value: %s", part)
				}
				if val < 0 || val > 63 {
					return fmt.Errorf("DSCP value must be 0-63, got %d", val)
				}
				dscpValues = append(dscpValues, val)
			}
		}

		queue := &spec.QoSQueue{
			Name:   addQueueName,
			Type:   addQueueType,
			Weight: addQueueWeight,
			DSCP:   dscpValues,
			ECN:    addQueueECN,
		}

		// Extend the queue slice if needed
		for len(policy.Queues) <= queueID {
			policy.Queues = append(policy.Queues, nil)
		}

		if policy.Queues[queueID] != nil {
			return fmt.Errorf("queue %d already exists in policy '%s'; remove it first", queueID, policyName)
		}

		fmt.Printf("Queue %d (%s) for policy '%s'\n", queueID, addQueueType, policyName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		policy.Queues[queueID] = queue

		if err := app.net.SaveQoSPolicy(policyName, policy); err != nil {
			return fmt.Errorf("saving QoS policy: %w", err)
		}

		fmt.Printf("Added queue %d to QoS policy '%s'\n", queueID, policyName)
		return nil
	},
}

var qosRemoveQueueCmd = &cobra.Command{
	Use:   "remove-queue <policy-name> <queue-id>",
	Short: "Remove a queue from a QoS policy",
	Long: `Remove a queue from a QoS policy in network.json.

Examples:
  newtron qos remove-queue my-policy 1`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		policyName := args[0]

		var queueID int
		if _, err := fmt.Sscanf(args[1], "%d", &queueID); err != nil {
			return fmt.Errorf("invalid queue-id: %s", args[1])
		}

		authCtx := auth.NewContext().WithResource(policyName)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		policy, err := app.net.GetQoSPolicy(policyName)
		if err != nil {
			return err
		}

		if queueID < 0 || queueID >= len(policy.Queues) || policy.Queues[queueID] == nil {
			return fmt.Errorf("queue %d not found in policy '%s'", queueID, policyName)
		}

		fmt.Printf("Removing queue %d from policy '%s'\n", queueID, policyName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		policy.Queues[queueID] = nil

		// Trim trailing nil entries
		for len(policy.Queues) > 0 && policy.Queues[len(policy.Queues)-1] == nil {
			policy.Queues = policy.Queues[:len(policy.Queues)-1]
		}

		if err := app.net.SaveQoSPolicy(policyName, policy); err != nil {
			return fmt.Errorf("saving QoS policy: %w", err)
		}

		fmt.Printf("Removed queue %d from QoS policy '%s'\n", queueID, policyName)
		return nil
	},
}

var qosApplyCmd = &cobra.Command{
	Use:   "apply <interface> <policy-name>",
	Short: "Apply a QoS policy to an interface",
	Long: `Apply a QoS policy to an interface on the device.

This writes QoS entries to CONFIG_DB for the specified interface.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny qos apply Ethernet0 8q-datacenter -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]
		policyName := args[1]

		policy, err := app.net.GetQoSPolicy(policyName)
		if err != nil {
			return err
		}

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(intfName)
			if err := checkExecutePermission(auth.PermQoSModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.ApplyQoS(ctx, intfName, policyName, policy)
			if err != nil {
				return nil, fmt.Errorf("applying QoS: %w", err)
			}
			return cs, nil
		})
	},
}

var qosRemoveCmd = &cobra.Command{
	Use:   "remove <interface>",
	Short: "Remove QoS configuration from an interface",
	Long: `Remove QoS configuration from an interface on the device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny qos remove Ethernet0 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intfName := args[0]

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(intfName)
			if err := checkExecutePermission(auth.PermQoSModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.RemoveQoS(ctx, intfName)
			if err != nil {
				return nil, fmt.Errorf("removing QoS: %w", err)
			}
			return cs, nil
		})
	},
}

func init() {
	qosCreateCmd.Flags().StringVar(&qosCreateDescription, "description", "", "Policy description")

	qosAddQueueCmd.Flags().StringVar(&addQueueType, "type", "", "Queue type (dwrr, strict)")
	qosAddQueueCmd.Flags().IntVar(&addQueueWeight, "weight", 0, "DWRR weight (percentage)")
	qosAddQueueCmd.Flags().StringVar(&addQueueDSCP, "dscp", "", "Comma-separated DSCP values (0-63)")
	qosAddQueueCmd.Flags().StringVar(&addQueueName, "name", "", "Queue name")
	qosAddQueueCmd.Flags().BoolVar(&addQueueECN, "ecn", false, "Enable ECN/WRED")

	qosCmd.AddCommand(qosListCmd)
	qosCmd.AddCommand(qosShowCmd)
	qosCmd.AddCommand(qosCreateCmd)
	qosCmd.AddCommand(qosDeleteCmd)
	qosCmd.AddCommand(qosAddQueueCmd)
	qosCmd.AddCommand(qosRemoveQueueCmd)
	qosCmd.AddCommand(qosApplyCmd)
	qosCmd.AddCommand(qosRemoveCmd)
}
