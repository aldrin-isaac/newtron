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

var lagCmd = &cobra.Command{
	Use:   "lag",
	Short: "Manage link aggregation groups (LAG/PortChannel)",
	Long: `Manage link aggregation groups.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny lag list
  newtron -D leaf1-ny lag show PortChannel100
  newtron -D leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4
  newtron -D leaf1-ny lag add-interface PortChannel100 Ethernet8
  newtron -D leaf1-ny lag remove-interface PortChannel100 Ethernet8
  newtron -D leaf1-ny lag status`,
}

var lagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all LAGs on the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		entries, err := app.client.ListLAGs(app.deviceName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			// Extract names for the list view
			var names []string
			for _, e := range entries {
				names = append(names, e.Name)
			}
			return json.NewEncoder(os.Stdout).Encode(names)
		}

		if len(entries) == 0 {
			fmt.Println("No LAGs configured")
			return nil
		}

		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name < entries[j].Name
		})

		t := cli.NewTable("NAME", "STATUS", "MEMBERS", "ACTIVE")

		for _, pc := range entries {
			status := formatAdminStatus(pc.AdminStatus)
			t.Row(
				pc.Name,
				status,
				strings.Join(pc.Members, ","),
				fmt.Sprintf("%d/%d", len(pc.ActiveMembers), len(pc.Members)),
			)
		}
		t.Flush()

		return nil
	},
}

var lagShowCmd = &cobra.Command{
	Use:   "show <lag-name>",
	Short: "Show detailed LAG information",
	Long: `Show detailed information about a single LAG.

Includes name, status, members, active members, MTU, and min-links.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny lag show PortChannel100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]

		if err := requireDevice(); err != nil {
			return err
		}

		pc, err := app.client.ShowLAGDetail(app.deviceName, lagName)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(pc)
		}

		fmt.Printf("LAG: %s\n", bold(pc.Name))

		fmt.Printf("Admin Status: %s\n", formatAdminStatus(pc.AdminStatus))

		if len(pc.Members) > 0 {
			fmt.Printf("Members: %s\n", strings.Join(pc.Members, ", "))
		} else {
			fmt.Println("Members: (none)")
		}

		if len(pc.ActiveMembers) > 0 {
			fmt.Printf("Active Members: %s (%d/%d)\n",
				strings.Join(pc.ActiveMembers, ", "),
				len(pc.ActiveMembers),
				len(pc.Members),
			)
		} else {
			fmt.Printf("Active Members: (none) (0/%d)\n", len(pc.Members))
		}

		if pc.MTU > 0 {
			fmt.Printf("MTU: %d\n", pc.MTU)
		}

		return nil
	},
}

var lagStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all LAGs with operational state",
	Long: `Show all LAGs with their operational state.

Combines config and STATE_DB information.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny lag status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		statuses, err := app.client.ListLAGs(app.deviceName)
		if err != nil {
			return err
		}

		if len(statuses) == 0 {
			fmt.Println("No LAGs configured")
			return nil
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(statuses)
		}

		t := cli.NewTable("NAME", "ADMIN", "OPER", "MEMBERS", "ACTIVE", "MTU")

		for _, s := range statuses {
			admin := formatAdminStatus(s.AdminStatus)

			oper := formatOperStatus(s.OperStatus)
			if s.OperStatus == "" {
				oper = "-"
			}

			mtu := dashInt(s.MTU)

			t.Row(
				s.Name,
				admin,
				oper,
				strings.Join(s.Members, ","),
				fmt.Sprintf("%d/%d", len(s.ActiveMembers), len(s.Members)),
				mtu,
			)
		}
		t.Flush()

		return nil
	},
}

var (
	lagMembers  string
	lagMinLinks int
	lagFastRate bool
	lagMTU      int
)

var lagCreateCmd = &cobra.Command{
	Use:   "create <lag-name>",
	Short: "Create a new LAG",
	Long: `Create a new link aggregation group.

The LAG name should be in the format PortChannelN (e.g., PortChannel100).

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4
  newtron -D leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4 --fast-rate -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]

		if lagMembers == "" {
			return fmt.Errorf("--members is required")
		}
		members := strings.Split(lagMembers, ",")
		for i := range members {
			members[i] = strings.TrimSpace(members[i])
		}

		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.CreatePortChannel(app.deviceName, newtron.PortChannelCreateRequest{
			Name:     lagName,
			Members:  members,
			MinLinks: lagMinLinks,
			FastRate: lagFastRate,
			MTU:      lagMTU,
		}, execOpts()))
	},
}

var lagAddInterfaceCmd = &cobra.Command{
	Use:   "add-interface <lag-name> <interface>",
	Short: "Add an interface to a LAG",
	Long: `Add a member interface to a LAG.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny lag add-interface PortChannel100 Ethernet8`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		memberName := args[1]
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.AddPortChannelMember(app.deviceName, lagName, memberName, execOpts()))
	},
}

var lagRemoveInterfaceCmd = &cobra.Command{
	Use:   "remove-interface <lag-name> <interface>",
	Short: "Remove an interface from a LAG",
	Long: `Remove a member interface from a LAG.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny lag remove-interface PortChannel100 Ethernet8`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		memberName := args[1]
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.RemovePortChannelMember(app.deviceName, lagName, memberName, execOpts()))
	},
}

var lagDeleteCmd = &cobra.Command{
	Use:   "delete <lag-name>",
	Short: "Delete a LAG",
	Long: `Delete a link aggregation group.

Requires -D (device) flag.

Examples:
  newtron -D leaf1-ny lag delete PortChannel100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		if err := requireDevice(); err != nil {
			return err
		}
		return displayWriteResult(app.client.DeletePortChannel(app.deviceName, lagName, execOpts()))
	},
}

func init() {
	lagCreateCmd.Flags().StringVar(&lagMembers, "members", "", "Comma-separated list of member interfaces (required)")
	lagCreateCmd.Flags().IntVar(&lagMinLinks, "min-links", 1, "Minimum links required")
	lagCreateCmd.Flags().BoolVar(&lagFastRate, "fast-rate", true, "Use LACP fast rate (1s vs 30s)")
	lagCreateCmd.Flags().IntVar(&lagMTU, "mtu", 9100, "MTU size")

	lagCmd.AddCommand(lagListCmd)
	lagCmd.AddCommand(lagShowCmd)
	lagCmd.AddCommand(lagStatusCmd)
	lagCmd.AddCommand(lagCreateCmd)
	lagCmd.AddCommand(lagDeleteCmd)
	lagCmd.AddCommand(lagAddInterfaceCmd)
	lagCmd.AddCommand(lagRemoveInterfaceCmd)
}
