package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/network"
)

var aclCmd = &cobra.Command{
	Use:   "acl",
	Short: "Manage Access Control Lists",
	Long: `Manage ACLs on SONiC devices.

Requires -d (device) flag.

Note: ACLs are typically managed through services. Use these commands
for manual ACL configuration outside the service model.

Examples:
  newtron -d leaf1-ny acl list
  newtron -d leaf1-ny acl show CUSTOM-ACL
  newtron -d leaf1-ny acl create CUSTOM-ACL --type L3 --stage ingress --interfaces Ethernet0
  newtron -d leaf1-ny acl add-rule CUSTOM-ACL RULE_10 --priority 9999 --src-ip 10.0.0.0/8 --action permit
  newtron -d leaf1-ny acl delete CUSTOM-ACL
  newtron -d leaf1-ny acl bind CUSTOM-ACL Ethernet0 --direction ingress`,
}

var aclListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all ACL tables",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		configDB := dev.ConfigDB()
		if configDB == nil {
			fmt.Println("Not connected to device config_db")
			return nil
		}

		// Count rules per ACL table (single pass)
		ruleCounts := make(map[string]int, len(configDB.ACLTable))
		for ruleKey := range configDB.ACLRule {
			if i := strings.IndexByte(ruleKey, '|'); i >= 0 {
				ruleCounts[ruleKey[:i]]++
			}
		}

		if app.jsonOutput {
			type aclSummary struct {
				Name       string `json:"name"`
				Type       string `json:"type"`
				Stage      string `json:"stage"`
				Interfaces string `json:"interfaces"`
				RuleCount  int    `json:"rule_count"`
			}
			var acls []aclSummary
			for name, table := range configDB.ACLTable {
				acls = append(acls, aclSummary{
					Name:       name,
					Type:       table.Type,
					Stage:      table.Stage,
					Interfaces: table.Ports,
					RuleCount:  ruleCounts[name],
				})
			}
			return json.NewEncoder(os.Stdout).Encode(acls)
		}

		if len(configDB.ACLTable) == 0 {
			fmt.Println("No ACL tables configured")
			return nil
		}

		t := cli.NewTable("NAME", "TYPE", "STAGE", "INTERFACES", "RULES")

		for name, table := range configDB.ACLTable {
			t.Row(name, table.Type, table.Stage, table.Ports, fmt.Sprintf("%d", ruleCounts[name]))
		}
		t.Flush()

		return nil
	},
}

var aclShowCmd = &cobra.Command{
	Use:   "show <acl-name>",
	Short: "Show ACL table details and rules",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		configDB := dev.ConfigDB()
		if configDB == nil {
			return fmt.Errorf("not connected to device config_db")
		}

		table, ok := configDB.ACLTable[aclName]
		if !ok {
			return fmt.Errorf("ACL table '%s' not found", aclName)
		}

		if app.jsonOutput {
			type ruleDetail struct {
				Name       string `json:"name"`
				Priority   string `json:"priority"`
				Action     string `json:"action"`
				SrcIP      string `json:"src_ip,omitempty"`
				DstIP      string `json:"dst_ip,omitempty"`
				Protocol   string `json:"protocol,omitempty"`
				SrcPort    string `json:"src_port,omitempty"`
				DstPort    string `json:"dst_port,omitempty"`
			}
			var rules []ruleDetail
			for ruleKey, rule := range configDB.ACLRule {
				if !strings.HasPrefix(ruleKey, aclName+"|") {
					continue
				}
				rules = append(rules, ruleDetail{
					Name:     strings.TrimPrefix(ruleKey, aclName+"|"),
					Priority: rule.Priority,
					Action:   rule.PacketAction,
					SrcIP:    rule.SrcIP,
					DstIP:    rule.DstIP,
					Protocol: rule.IPProtocol,
					SrcPort:  rule.L4SrcPort,
					DstPort:  rule.L4DstPort,
				})
			}
			data := map[string]any{
				"name":        aclName,
				"type":        table.Type,
				"stage":       table.Stage,
				"interfaces":  table.Ports,
				"description": table.PolicyDesc,
				"rules":       rules,
			}
			return json.NewEncoder(os.Stdout).Encode(data)
		}

		fmt.Printf("ACL Table: %s\n", aclName)
		fmt.Printf("  Type:  %s\n", table.Type)
		fmt.Printf("  Stage: %s\n", table.Stage)
		fmt.Printf("  Interfaces: %s\n", table.Ports)
		fmt.Printf("  Description: %s\n", table.PolicyDesc)

		// Show rules
		fmt.Println("\nRules:")
		t := cli.NewTable("RULE", "PRIORITY", "ACTION", "SRC_IP", "DST_IP", "PROTOCOL", "DST_PORT").WithPrefix("  ")

		for ruleKey, rule := range configDB.ACLRule {
			if !strings.HasPrefix(ruleKey, aclName+"|") {
				continue
			}
			ruleName := strings.TrimPrefix(ruleKey, aclName+"|")
			t.Row(ruleName, rule.Priority, rule.PacketAction,
				defaultStr(rule.SrcIP, "-"),
				defaultStr(rule.DstIP, "-"),
				defaultStr(rule.IPProtocol, "-"),
				defaultStr(rule.L4DstPort, "-"))
		}
		t.Flush()

		return nil
	},
}

var (
	aclType        string
	aclStage       string
	aclInterfaces  string
	aclDescription string
)

var aclCreateCmd = &cobra.Command{
	Use:   "create <acl-name>",
	Short: "Create a new ACL table",
	Long: `Create a new ACL table.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl create CUSTOM-ACL --type L3 --stage ingress --interfaces Ethernet0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]

		if aclType == "" {
			return fmt.Errorf("--type is required (L3, L3V6, MIRROR, MIRRORV6)")
		}
		if aclStage == "" {
			return fmt.Errorf("--stage is required (ingress, egress)")
		}

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.CreateACLTable(ctx, aclName, network.ACLTableConfig{
				Type:        aclType,
				Stage:       aclStage,
				Ports:       aclInterfaces,
				Description: aclDescription,
			})
			if err != nil {
				return nil, fmt.Errorf("creating ACL table: %w", err)
			}
			return cs, nil
		})
	},
}

var (
	rulePriority int
	ruleSrcIP    string
	ruleDstIP    string
	ruleProtocol string
	ruleSrcPort  string
	ruleDstPort  string
	ruleAction   string
)

var aclAddRuleCmd = &cobra.Command{
	Use:   "add-rule <acl-name> <rule-name>",
	Short: "Add a rule to an ACL table",
	Long: `Add a rule to an ACL table.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl add-rule CUSTOM-ACL RULE_10 --priority 9999 --src-ip 10.0.0.0/8 --action permit`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		ruleName := args[1]

		if ruleAction == "" {
			return fmt.Errorf("--action is required (permit, deny)")
		}

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return nil, err
			}
			if !dev.ACLTableExists(aclName) {
				return nil, fmt.Errorf("ACL table '%s' not found", aclName)
			}
			cs, err := dev.AddACLRule(ctx, aclName, ruleName, network.ACLRuleConfig{
				Priority: rulePriority,
				SrcIP:    ruleSrcIP,
				DstIP:    ruleDstIP,
				Protocol: ruleProtocol,
				SrcPort:  ruleSrcPort,
				DstPort:  ruleDstPort,
				Action:   ruleAction,
			})
			if err != nil {
				return nil, fmt.Errorf("adding ACL rule: %w", err)
			}
			return cs, nil
		})
	},
}

var aclDeleteRuleCmd = &cobra.Command{
	Use:   "delete-rule <acl-name> <rule-name>",
	Short: "Delete a rule from an ACL table",
	Long: `Delete a rule from an ACL table.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl delete-rule CUSTOM-ACL RULE_10 -x`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		ruleName := args[1]
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return nil, err
			}
			if !dev.ACLTableExists(aclName) {
				return nil, fmt.Errorf("ACL table '%s' not found", aclName)
			}
			cs, err := dev.DeleteACLRule(ctx, aclName, ruleName)
			if err != nil {
				return nil, fmt.Errorf("deleting ACL rule: %w", err)
			}
			return cs, nil
		})
	},
}

var aclDeleteCmd = &cobra.Command{
	Use:   "delete <acl-name>",
	Short: "Delete an ACL table and its rules",
	Long: `Delete an ACL table and its rules.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl delete CUSTOM-ACL`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return nil, err
			}
			if !dev.ACLTableExists(aclName) {
				return nil, fmt.Errorf("ACL table '%s' not found", aclName)
			}
			cs, err := dev.DeleteACLTable(ctx, aclName)
			if err != nil {
				return nil, fmt.Errorf("deleting ACL table: %w", err)
			}
			return cs, nil
		})
	},
}

var aclBindDirection string

var aclBindCmd = &cobra.Command{
	Use:   "bind <acl-name> <interface>",
	Short: "Bind an ACL to an interface",
	Long: `Bind an ACL to an interface.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl bind CUSTOM-ACL Ethernet0 --direction ingress`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		interfaceName := args[1]

		if aclBindDirection == "" {
			aclBindDirection = "ingress"
		}

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return nil, err
			}
			if !dev.ACLTableExists(aclName) {
				return nil, fmt.Errorf("ACL table '%s' not found", aclName)
			}
			if !dev.InterfaceExists(interfaceName) {
				return nil, fmt.Errorf("interface '%s' not found", interfaceName)
			}
			intf, err := dev.GetInterface(interfaceName)
			if err != nil {
				return nil, fmt.Errorf("getting interface: %w", err)
			}
			cs, err := intf.BindACL(ctx, aclName, aclBindDirection)
			if err != nil {
				return nil, fmt.Errorf("binding ACL: %w", err)
			}
			return cs, nil
		})
	},
}

var aclUnbindCmd = &cobra.Command{
	Use:   "unbind <acl-name> <interface>",
	Short: "Unbind an ACL from an interface",
	Long: `Unbind an ACL from an interface.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl unbind CUSTOM-ACL Ethernet0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		interfaceName := args[1]
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return nil, err
			}
			if !dev.ACLTableExists(aclName) {
				return nil, fmt.Errorf("ACL table '%s' not found", aclName)
			}
			cs, err := dev.UnbindACLFromInterface(ctx, aclName, interfaceName)
			if err != nil {
				return nil, fmt.Errorf("unbinding ACL: %w", err)
			}
			return cs, nil
		})
	},
}

func init() {
	aclCreateCmd.Flags().StringVar(&aclType, "type", "", "ACL type (L3, L3V6, MIRROR, MIRRORV6)")
	aclCreateCmd.Flags().StringVar(&aclStage, "stage", "", "ACL stage (ingress, egress)")
	aclCreateCmd.Flags().StringVar(&aclInterfaces, "interfaces", "", "Comma-separated list of interfaces to bind")
	aclCreateCmd.Flags().StringVar(&aclDescription, "description", "", "ACL description")

	aclAddRuleCmd.Flags().IntVar(&rulePriority, "priority", 9999, "Rule priority (higher = evaluated first)")
	aclAddRuleCmd.Flags().StringVar(&ruleSrcIP, "src-ip", "", "Source IP/CIDR")
	aclAddRuleCmd.Flags().StringVar(&ruleDstIP, "dst-ip", "", "Destination IP/CIDR")
	aclAddRuleCmd.Flags().StringVar(&ruleProtocol, "protocol", "", "IP protocol (tcp, udp, icmp, or number)")
	aclAddRuleCmd.Flags().StringVar(&ruleSrcPort, "src-port", "", "Source port or range")
	aclAddRuleCmd.Flags().StringVar(&ruleDstPort, "dst-port", "", "Destination port or range")
	aclAddRuleCmd.Flags().StringVar(&ruleAction, "action", "", "Action (permit, deny)")

	aclBindCmd.Flags().StringVar(&aclBindDirection, "direction", "ingress", "Direction (ingress, egress)")

	aclCmd.AddCommand(aclListCmd)
	aclCmd.AddCommand(aclShowCmd)
	aclCmd.AddCommand(aclCreateCmd)
	aclCmd.AddCommand(aclAddRuleCmd)
	aclCmd.AddCommand(aclDeleteRuleCmd)
	aclCmd.AddCommand(aclDeleteCmd)
	aclCmd.AddCommand(aclBindCmd)
	aclCmd.AddCommand(aclUnbindCmd)
}
