package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var routePolicyCmd = &cobra.Command{
	Use:   "route-policy",
	Short: "Manage route policy templates",
	Long: `Manage route policy templates in network.json (spec authoring).

Route policies define reusable BGP policy rule sets that can be referenced
by services and VRF configurations.

All commands operate on network.json (no device needed).

Examples:
  newtron route-policy list
  newtron route-policy show customer-export
  newtron route-policy create customer-export --description "Customer export policy"
  newtron route-policy add-rule customer-export --seq 10 --action permit --prefix-list rfc1918
  newtron route-policy add-rule customer-export --seq 20 --action deny
  newtron route-policy remove-rule customer-export 10
  newtron route-policy delete customer-export`,
}

var routePolicyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all route policy templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := app.client.ListRoutePolicies()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(names)
		}

		if len(names) == 0 {
			fmt.Println("No route policy templates defined")
			return nil
		}

		sort.Strings(names)

		t := cli.NewTable("NAME", "RULES", "DESCRIPTION")

		for _, name := range names {
			detail, err := app.client.ShowRoutePolicy(name)
			if err != nil {
				continue
			}
			t.Row(name, fmt.Sprintf("%d", len(detail.Rules)), defaultStr(detail.Description, "-"))
		}
		t.Flush()

		return nil
	},
}

var routePolicyShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show route policy template details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		detail, err := app.client.ShowRoutePolicy(name)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(detail)
		}

		fmt.Printf("Route Policy: %s\n", bold(name))
		if detail.Description != "" {
			fmt.Printf("Description: %s\n", detail.Description)
		}
		fmt.Printf("Rules: %d\n", len(detail.Rules))
		fmt.Println()

		if len(detail.Rules) == 0 {
			fmt.Println("  (no rules configured)")
			return nil
		}

		t := cli.NewTable("SEQ", "ACTION", "PREFIX_LIST", "COMMUNITY", "SET").WithPrefix("  ")

		for _, r := range detail.Rules {
			t.Row(
				fmt.Sprintf("%d", r.Sequence),
				r.Action,
				defaultStr(r.PrefixList, "-"),
				defaultStr(r.Community, "-"),
				formatRoutePolicySet(r.Set),
			)
		}
		t.Flush()

		return nil
	},
}

// formatRoutePolicySet formats a RoutePolicySetSpec for table display.
// Shows only non-zero values, or "-" if no set actions are configured.
func formatRoutePolicySet(set *newtron.RoutePolicySetSpec) string {
	if set == nil {
		return "-"
	}
	var parts []string
	if set.LocalPref != 0 {
		parts = append(parts, fmt.Sprintf("local_pref=%d", set.LocalPref))
	}
	if set.Community != "" {
		parts = append(parts, fmt.Sprintf("community=%s", set.Community))
	}
	if set.MED != 0 {
		parts = append(parts, fmt.Sprintf("med=%d", set.MED))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

var routePolicyCreateDescription string

var routePolicyCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new route policy template",
	Long: `Create a new route policy template in network.json.

This creates an empty route policy. Use 'route-policy add-rule' to add rules.

Examples:
  newtron route-policy create customer-export --description "Customer export policy"
  newtron route-policy create default-import`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		fmt.Printf("Route Policy: %s\n", name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.client.CreateRoutePolicy(newtron.CreateRoutePolicyRequest{
			Name:        name,
			Description: routePolicyCreateDescription,
		}, execOpts())
	},
}

var routePolicyDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a route policy template",
	Long: `Delete a route policy template from network.json.

Fails if any service or VRF references the policy.

Examples:
  newtron route-policy delete customer-export`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		fmt.Printf("Deleting route policy: %s\n", name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.client.DeleteRoutePolicy(name, execOpts())
	},
}

var (
	routePolicyRuleSeq          int
	routePolicyRuleAction       string
	routePolicyRulePrefixList   string
	routePolicyRuleCommunity    string
	routePolicyRuleSetLocalPref int
	routePolicyRuleSetCommunity string
	routePolicyRuleSetMED       int
)

var routePolicyAddRuleCmd = &cobra.Command{
	Use:   "add-rule <name>",
	Short: "Add a rule to a route policy template",
	Long: `Add a rule to a route policy template in network.json.

The --seq and --action flags are required.
Sequence number determines rule evaluation order (lower = matched first).

Examples:
  newtron route-policy add-rule customer-export --seq 10 --action permit --prefix-list rfc1918
  newtron route-policy add-rule customer-export --seq 20 --action permit --set-local-pref 200
  newtron route-policy add-rule customer-export --seq 30 --action deny`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if routePolicyRuleSeq <= 0 {
			return fmt.Errorf("--seq is required (positive integer)")
		}
		if routePolicyRuleAction == "" {
			return fmt.Errorf("--action is required (permit, deny)")
		}
		if routePolicyRuleAction != "permit" && routePolicyRuleAction != "deny" {
			return fmt.Errorf("--action must be 'permit' or 'deny', got '%s'", routePolicyRuleAction)
		}

		fmt.Printf("Rule: seq %d, action %s, policy '%s'\n", routePolicyRuleSeq, routePolicyRuleAction, name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		req := newtron.AddRoutePolicyRuleRequest{
			Policy:     name,
			Sequence:   routePolicyRuleSeq,
			Action:     routePolicyRuleAction,
			PrefixList: routePolicyRulePrefixList,
			Community:  routePolicyRuleCommunity,
		}

		if routePolicyRuleSetLocalPref != 0 || routePolicyRuleSetCommunity != "" || routePolicyRuleSetMED != 0 {
			req.Set = &newtron.RoutePolicySetSpec{
				LocalPref: routePolicyRuleSetLocalPref,
				Community: routePolicyRuleSetCommunity,
				MED:       routePolicyRuleSetMED,
			}
		}

		return app.client.AddRoutePolicyRule(req, execOpts())
	},
}

var routePolicyRemoveRuleCmd = &cobra.Command{
	Use:   "remove-rule <name> <seq>",
	Short: "Remove a rule from a route policy template",
	Long: `Remove a rule from a route policy template by its sequence number.

Examples:
  newtron route-policy remove-rule customer-export 10`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		var seq int
		if _, err := fmt.Sscanf(args[1], "%d", &seq); err != nil {
			return fmt.Errorf("invalid sequence number: %s", args[1])
		}

		fmt.Printf("Removing rule (seq %d) from route policy '%s'\n", seq, name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.client.RemoveRoutePolicyRule(name, seq, execOpts())
	},
}

func init() {
	routePolicyCreateCmd.Flags().StringVar(&routePolicyCreateDescription, "description", "", "Route policy description")

	routePolicyAddRuleCmd.Flags().IntVar(&routePolicyRuleSeq, "seq", 0, "Rule sequence number (required)")
	routePolicyAddRuleCmd.Flags().StringVar(&routePolicyRuleAction, "action", "", "Rule action (permit, deny)")
	routePolicyAddRuleCmd.Flags().StringVar(&routePolicyRulePrefixList, "prefix-list", "", "Match prefix list name")
	routePolicyAddRuleCmd.Flags().StringVar(&routePolicyRuleCommunity, "community", "", "Match BGP community")
	routePolicyAddRuleCmd.Flags().IntVar(&routePolicyRuleSetLocalPref, "set-local-pref", 0, "Set local preference")
	routePolicyAddRuleCmd.Flags().StringVar(&routePolicyRuleSetCommunity, "set-community", "", "Set BGP community")
	routePolicyAddRuleCmd.Flags().IntVar(&routePolicyRuleSetMED, "set-med", 0, "Set MED (metric)")

	routePolicyCmd.AddCommand(routePolicyListCmd)
	routePolicyCmd.AddCommand(routePolicyShowCmd)
	routePolicyCmd.AddCommand(routePolicyCreateCmd)
	routePolicyCmd.AddCommand(routePolicyDeleteCmd)
	routePolicyCmd.AddCommand(routePolicyAddRuleCmd)
	routePolicyCmd.AddCommand(routePolicyRemoveRuleCmd)
}
