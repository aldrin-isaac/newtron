package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
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
  newtron -d leaf1-ny acl create CUSTOM-ACL --type L3 --stage ingress --ports Ethernet0
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

		if len(configDB.ACLTable) == 0 {
			fmt.Println("No ACL tables configured")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tSTAGE\tPORTS\tRULES")
		fmt.Fprintln(w, "----\t----\t-----\t-----\t-----")

		for name, table := range configDB.ACLTable {
			// Count rules for this table
			ruleCount := 0
			for ruleKey := range configDB.ACLRule {
				if strings.HasPrefix(ruleKey, name+"|") {
					ruleCount++
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
				name, table.Type, table.Stage, table.Ports, ruleCount)
		}
		w.Flush()

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

		fmt.Printf("ACL Table: %s\n", aclName)
		fmt.Printf("  Type:  %s\n", table.Type)
		fmt.Printf("  Stage: %s\n", table.Stage)
		fmt.Printf("  Ports: %s\n", table.Ports)
		fmt.Printf("  Description: %s\n", table.PolicyDesc)

		// Show rules
		fmt.Println("\nRules:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  RULE\tPRIORITY\tACTION\tSRC_IP\tDST_IP\tPROTOCOL\tDST_PORT")
		fmt.Fprintln(w, "  ----\t--------\t------\t------\t------\t--------\t--------")

		for ruleKey, rule := range configDB.ACLRule {
			if !strings.HasPrefix(ruleKey, aclName+"|") {
				continue
			}
			ruleName := strings.TrimPrefix(ruleKey, aclName+"|")
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				ruleName, rule.Priority, rule.PacketAction,
				defaultStr(rule.SrcIP, "-"),
				defaultStr(rule.DstIP, "-"),
				defaultStr(rule.IPProtocol, "-"),
				defaultStr(rule.L4DstPort, "-"))
		}
		w.Flush()

		return nil
	},
}

var (
	aclType        string
	aclStage       string
	aclPorts       string
	aclDescription string
)

var aclCreateCmd = &cobra.Command{
	Use:   "create <acl-name>",
	Short: "Create a new ACL table",
	Long: `Create a new ACL table.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl create CUSTOM-ACL --type L3 --stage ingress --ports Ethernet0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		if aclType == "" {
			return fmt.Errorf("--type is required (L3, L3V6, MIRROR, MIRRORV6)")
		}
		if aclStage == "" {
			return fmt.Errorf("--stage is required (ingress, egress)")
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.CreateACLTable(ctx, aclName, network.ACLTableConfig{
			Type:        aclType,
			Stage:       aclStage,
			Ports:       aclPorts,
			Description: aclDescription,
		})
		if err != nil {
			return fmt.Errorf("creating ACL table: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
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

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		if ruleAction == "" {
			return fmt.Errorf("--action is required (permit, deny)")
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !dev.ACLTableExists(aclName) {
			return fmt.Errorf("ACL table '%s' not found", aclName)
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.AddACLRule(ctx, aclName, ruleName, network.ACLRuleConfig{
			Priority: rulePriority,
			SrcIP:    ruleSrcIP,
			DstIP:    ruleDstIP,
			Protocol: ruleProtocol,
			SrcPort:  ruleSrcPort,
			DstPort:  ruleDstPort,
			Action:   ruleAction,
		})
		if err != nil {
			return fmt.Errorf("adding ACL rule: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
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

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !dev.ACLTableExists(aclName) {
			return fmt.Errorf("ACL table '%s' not found", aclName)
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.DeleteACLRule(ctx, aclName, ruleName)
		if err != nil {
			return fmt.Errorf("deleting ACL rule: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
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

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !dev.ACLTableExists(aclName) {
			return fmt.Errorf("ACL table '%s' not found", aclName)
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.DeleteACLTable(ctx, aclName)
		if err != nil {
			return fmt.Errorf("deleting ACL table: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var aclBindDirection string

var aclBindCmd = &cobra.Command{
	Use:   "bind <acl-name> <port>",
	Short: "Bind an ACL to a port",
	Long: `Bind an ACL to a port.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl bind CUSTOM-ACL Ethernet0 --direction ingress`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		portName := args[1]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		if aclBindDirection == "" {
			aclBindDirection = "ingress"
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !dev.ACLTableExists(aclName) {
			return fmt.Errorf("ACL table '%s' not found", aclName)
		}
		if !dev.InterfaceExists(portName) {
			return fmt.Errorf("interface '%s' not found", portName)
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		intf, err := dev.GetInterface(portName)
		if err != nil {
			return fmt.Errorf("getting interface: %w", err)
		}

		changeSet, err := intf.BindACL(ctx, aclName, aclBindDirection)
		if err != nil {
			return fmt.Errorf("binding ACL: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

var aclUnbindCmd = &cobra.Command{
	Use:   "unbind <acl-name> <port>",
	Short: "Unbind an ACL from a port",
	Long: `Unbind an ACL from a port.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny acl unbind CUSTOM-ACL Ethernet0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		aclName := args[0]
		portName := args[1]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(aclName)
		if err := checkExecutePermission(auth.PermACLModify, authCtx); err != nil {
			return err
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !dev.ACLTableExists(aclName) {
			return fmt.Errorf("ACL table '%s' not found", aclName)
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.UnbindACLFromPort(ctx, aclName, portName)
		if err != nil {
			return fmt.Errorf("unbinding ACL: %w", err)
		}

		fmt.Println("Changes to be applied:")
		fmt.Print(changeSet.String())

		if executeMode {
			if err := executeAndSave(ctx, changeSet, dev); err != nil {
				return err
			}
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func init() {
	aclCreateCmd.Flags().StringVar(&aclType, "type", "", "ACL type (L3, L3V6, MIRROR, MIRRORV6)")
	aclCreateCmd.Flags().StringVar(&aclStage, "stage", "", "ACL stage (ingress, egress)")
	aclCreateCmd.Flags().StringVar(&aclPorts, "ports", "", "Comma-separated list of ports to bind")
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
