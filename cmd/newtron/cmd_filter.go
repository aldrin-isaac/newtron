package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var filterCmd = &cobra.Command{
	Use:   "filter",
	Short: "Manage filter templates",
	Long: `Manage filter templates in network.json (spec authoring).

Filters define reusable ACL rule sets that can be referenced by services
via ingress_filter/egress_filter fields.

All commands operate on network.json (no device needed).

Examples:
  newtron filter list
  newtron filter show customer-ingress
  newtron filter create customer-ingress --type ipv4 --description "Customer ingress filter"
  newtron filter add-rule customer-ingress --priority 100 --action permit --src-ip 10.0.0.0/8
  newtron filter add-rule customer-ingress --priority 200 --action deny --protocol icmp
  newtron filter remove-rule customer-ingress 100
  newtron filter delete customer-ingress`,
}

var filterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all filter templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		filterNames := app.net.ListFilters()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(filterNames)
		}

		if len(filterNames) == 0 {
			fmt.Println("No filter templates defined")
			return nil
		}

		sort.Strings(filterNames)

		t := cli.NewTable("NAME", "TYPE", "RULES", "DESCRIPTION")

		for _, name := range filterNames {
			fs, err := app.net.ShowFilter(name)
			if err != nil {
				continue
			}
			t.Row(name, fs.Type, fmt.Sprintf("%d", len(fs.Rules)), fs.Description)
		}
		t.Flush()

		return nil
	},
}

var filterShowCmd = &cobra.Command{
	Use:   "show <filter-name>",
	Short: "Show filter template details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filterName := args[0]

		fs, err := app.net.ShowFilter(filterName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(fs)
		}

		fmt.Printf("Filter: %s\n", bold(filterName))
		fmt.Printf("Type: %s\n", fs.Type)
		if fs.Description != "" {
			fmt.Printf("Description: %s\n", fs.Description)
		}
		fmt.Printf("Rules: %d\n", len(fs.Rules))
		fmt.Println()

		if len(fs.Rules) == 0 {
			fmt.Println("  (no rules configured)")
			return nil
		}

		t := cli.NewTable("SEQ", "ACTION", "SRC_IP", "DST_IP", "PROTOCOL", "SRC_PORT", "DST_PORT", "DSCP").WithPrefix("  ")

		for _, r := range fs.Rules {
			t.Row(
				fmt.Sprintf("%d", r.Sequence),
				r.Action,
				defaultStr(r.SrcIP, defaultStr(r.SrcPrefixList, "-")),
				defaultStr(r.DstIP, defaultStr(r.DstPrefixList, "-")),
				defaultStr(r.Protocol, "-"),
				defaultStr(r.SrcPort, "-"),
				defaultStr(r.DstPort, "-"),
				defaultStr(r.DSCP, "-"),
			)
		}
		t.Flush()

		return nil
	},
}

var (
	filterCreateType        string
	filterCreateDescription string
)

var filterCreateCmd = &cobra.Command{
	Use:   "create <filter-name>",
	Short: "Create a new filter template",
	Long: `Create a new filter template in network.json.

This creates an empty filter. Use 'filter add-rule' to add rules.

Examples:
  newtron filter create customer-ingress --type ipv4 --description "Customer ingress filter"
  newtron filter create customer-v6-ingress --type ipv6`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filterName := args[0]

		if filterCreateType == "" {
			return fmt.Errorf("--type is required (ipv4, ipv6)")
		}
		if filterCreateType != "ipv4" && filterCreateType != "ipv6" {
			return fmt.Errorf("--type must be 'ipv4' or 'ipv6'")
		}

		fmt.Printf("Filter: %s (type: %s)\n", filterName, filterCreateType)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.net.CreateFilter(newtron.CreateFilterRequest{
			Name:        filterName,
			Type:        filterCreateType,
			Description: filterCreateDescription,
		}, newtron.ExecOpts{Execute: true})
	},
}

var filterDeleteCmd = &cobra.Command{
	Use:   "delete <filter-name>",
	Short: "Delete a filter template",
	Long: `Delete a filter template from network.json.

Fails if any service references the filter.

Examples:
  newtron filter delete customer-ingress`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filterName := args[0]

		authCtx := auth.NewContext().WithResource(filterName)
		if err := checkExecutePermission(auth.PermFilterDelete, authCtx); err != nil {
			return err
		}

		fmt.Printf("Deleting filter: %s\n", filterName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.net.DeleteFilter(filterName, newtron.ExecOpts{Execute: true})
	},
}

var (
	filterRulePriority      int
	filterRuleAction        string
	filterRuleSrcIP         string
	filterRuleDstIP         string
	filterRuleProtocol      string
	filterRuleSrcPort       string
	filterRuleDstPort       string
	filterRuleDSCP          string
	filterRuleSrcPrefixList string
	filterRuleDstPrefixList string
)

var filterAddRuleCmd = &cobra.Command{
	Use:   "add-rule <filter-name>",
	Short: "Add a rule to a filter template",
	Long: `Add a rule to a filter template in network.json.

The --priority and --action flags are required.
Priority determines rule evaluation order (lower = matched first).

Examples:
  newtron filter add-rule customer-ingress --priority 100 --action permit --src-ip 10.0.0.0/8
  newtron filter add-rule customer-ingress --priority 200 --action deny --protocol tcp --dst-port 22
  newtron filter add-rule customer-ingress --priority 300 --action permit --src-prefix-list rfc1918`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filterName := args[0]

		if filterRulePriority <= 0 {
			return fmt.Errorf("--priority is required (positive integer)")
		}
		if filterRuleAction == "" {
			return fmt.Errorf("--action is required (permit, deny)")
		}
		if filterRuleAction != "permit" && filterRuleAction != "deny" {
			return fmt.Errorf("--action must be 'permit' or 'deny', got '%s'", filterRuleAction)
		}

		authCtx := auth.NewContext().WithResource(filterName)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		fmt.Printf("Rule: priority %d, action %s, filter '%s'\n", filterRulePriority, filterRuleAction, filterName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.net.AddFilterRule(newtron.AddFilterRuleRequest{
			Filter:        filterName,
			Sequence:      filterRulePriority,
			Action:        filterRuleAction,
			SrcIP:         filterRuleSrcIP,
			DstIP:         filterRuleDstIP,
			Protocol:      filterRuleProtocol,
			SrcPort:       filterRuleSrcPort,
			DstPort:       filterRuleDstPort,
			DSCP:          filterRuleDSCP,
			SrcPrefixList: filterRuleSrcPrefixList,
			DstPrefixList: filterRuleDstPrefixList,
		}, newtron.ExecOpts{Execute: true})
	},
}

var filterRemoveRuleCmd = &cobra.Command{
	Use:   "remove-rule <filter-name> <priority>",
	Short: "Remove a rule from a filter template",
	Long: `Remove a rule from a filter template by its priority/sequence number.

Examples:
  newtron filter remove-rule customer-ingress 100`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		filterName := args[0]

		var priority int
		if _, err := fmt.Sscanf(args[1], "%d", &priority); err != nil {
			return fmt.Errorf("invalid priority: %s", args[1])
		}

		authCtx := auth.NewContext().WithResource(filterName)
		if err := checkExecutePermission(auth.PermSpecAuthor, authCtx); err != nil {
			return err
		}

		fmt.Printf("Removing rule (priority %d) from filter '%s'\n", priority, filterName)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.net.RemoveFilterRule(filterName, priority, newtron.ExecOpts{Execute: true})
	},
}

func init() {
	filterCreateCmd.Flags().StringVar(&filterCreateType, "type", "", "Filter type (ipv4, ipv6)")
	filterCreateCmd.Flags().StringVar(&filterCreateDescription, "description", "", "Filter description")

	filterAddRuleCmd.Flags().IntVar(&filterRulePriority, "priority", 0, "Rule priority/sequence number (required)")
	filterAddRuleCmd.Flags().StringVar(&filterRuleAction, "action", "", "Rule action (permit, deny)")
	filterAddRuleCmd.Flags().StringVar(&filterRuleSrcIP, "src-ip", "", "Source IP/CIDR")
	filterAddRuleCmd.Flags().StringVar(&filterRuleDstIP, "dst-ip", "", "Destination IP/CIDR")
	filterAddRuleCmd.Flags().StringVar(&filterRuleProtocol, "protocol", "", "IP protocol (tcp, udp, icmp, or number)")
	filterAddRuleCmd.Flags().StringVar(&filterRuleSrcPort, "src-port", "", "Source port or range")
	filterAddRuleCmd.Flags().StringVar(&filterRuleDstPort, "dst-port", "", "Destination port or range")
	filterAddRuleCmd.Flags().StringVar(&filterRuleDSCP, "dscp", "", "DSCP value")
	filterAddRuleCmd.Flags().StringVar(&filterRuleSrcPrefixList, "src-prefix-list", "", "Source prefix list name")
	filterAddRuleCmd.Flags().StringVar(&filterRuleDstPrefixList, "dst-prefix-list", "", "Destination prefix list name")
	filterCmd.AddCommand(filterListCmd)
	filterCmd.AddCommand(filterShowCmd)
	filterCmd.AddCommand(filterCreateCmd)
	filterCmd.AddCommand(filterDeleteCmd)
	filterCmd.AddCommand(filterAddRuleCmd)
	filterCmd.AddCommand(filterRemoveRuleCmd)
}
