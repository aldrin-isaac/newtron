package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
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
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		acls, err := n.ListACLs()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(acls)
		}

		if len(acls) == 0 {
			fmt.Println("No ACL tables configured")
			return nil
		}

		t := cli.NewTable("NAME", "TYPE", "STAGE", "INTERFACES", "RULES")

		for _, acl := range acls {
			t.Row(acl.Name, acl.Type, acl.Stage, acl.Interfaces, fmt.Sprintf("%d", acl.RuleCount))
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
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		detail, err := n.ShowACL(aclName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(detail)
		}

		fmt.Printf("ACL Table: %s\n", aclName)
		fmt.Printf("  Type:  %s\n", detail.Type)
		fmt.Printf("  Stage: %s\n", detail.Stage)
		fmt.Printf("  Interfaces: %s\n", detail.Interfaces)
		fmt.Printf("  Description: %s\n", detail.Description)

		// Show rules
		fmt.Println("\nRules:")
		t := cli.NewTable("RULE", "PRIORITY", "ACTION", "SRC_IP", "DST_IP", "PROTOCOL", "DST_PORT").WithPrefix("  ")

		for _, rule := range detail.Rules {
			t.Row(rule.Name, rule.Priority, rule.Action,
				defaultStr(rule.SrcIP, "-"),
				defaultStr(rule.DstIP, "-"),
				defaultStr(rule.Protocol, "-"),
				defaultStr(rule.DstPort, "-"))
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

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return err
			}
			if err := n.CreateACLTable(ctx, aclName, newtron.ACLTableConfig{
				Type:        aclType,
				Stage:       aclStage,
				Ports:       aclInterfaces,
				Description: aclDescription,
			}); err != nil {
				return fmt.Errorf("creating ACL table: %w", err)
			}
			return nil
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

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return err
			}
			if !n.ACLTableExists(aclName) {
				return fmt.Errorf("ACL table '%s' not found", aclName)
			}
			if err := n.AddACLRule(ctx, aclName, ruleName, newtron.ACLRuleConfig{
				Priority: rulePriority,
				SrcIP:    ruleSrcIP,
				DstIP:    ruleDstIP,
				Protocol: ruleProtocol,
				SrcPort:  ruleSrcPort,
				DstPort:  ruleDstPort,
				Action:   ruleAction,
			}); err != nil {
				return fmt.Errorf("adding ACL rule: %w", err)
			}
			return nil
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
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return err
			}
			if !n.ACLTableExists(aclName) {
				return fmt.Errorf("ACL table '%s' not found", aclName)
			}
			if err := n.RemoveACLRule(ctx, aclName, ruleName); err != nil {
				return fmt.Errorf("deleting ACL rule: %w", err)
			}
			return nil
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
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return err
			}
			if !n.ACLTableExists(aclName) {
				return fmt.Errorf("ACL table '%s' not found", aclName)
			}
			if err := n.DeleteACLTable(ctx, aclName); err != nil {
				return fmt.Errorf("deleting ACL table: %w", err)
			}
			return nil
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

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return err
			}
			if !n.ACLTableExists(aclName) {
				return fmt.Errorf("ACL table '%s' not found", aclName)
			}
			if !n.InterfaceExists(interfaceName) {
				return fmt.Errorf("interface '%s' not found", interfaceName)
			}
			iface, err := n.Interface(interfaceName)
			if err != nil {
				return fmt.Errorf("getting interface: %w", err)
			}
			if err := iface.BindACL(ctx, aclName, aclBindDirection); err != nil {
				return fmt.Errorf("binding ACL: %w", err)
			}
			return nil
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
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(aclName)
			if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
				return err
			}
			if !n.ACLTableExists(aclName) {
				return fmt.Errorf("ACL table '%s' not found", aclName)
			}
			iface, err := n.Interface(interfaceName)
			if err != nil {
				return fmt.Errorf("getting interface: %w", err)
			}
			if err := iface.UnbindACL(ctx, aclName); err != nil {
				return fmt.Errorf("unbinding ACL: %w", err)
			}
			return nil
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
