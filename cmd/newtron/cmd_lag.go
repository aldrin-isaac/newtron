package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var lagCmd = &cobra.Command{
	Use:   "lag",
	Short: "Manage link aggregation groups (LAG/PortChannel)",
	Long: `Manage link aggregation groups.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag list
  newtron -d leaf1-ny lag show PortChannel100
  newtron -d leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4
  newtron -d leaf1-ny lag add-interface PortChannel100 Ethernet8
  newtron -d leaf1-ny lag remove-interface PortChannel100 Ethernet8
  newtron -d leaf1-ny lag status`,
}

var lagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all LAGs on the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		portChannels := n.ListPortChannels()

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(portChannels)
		}

		if len(portChannels) == 0 {
			fmt.Println("No LAGs configured")
			return nil
		}

		sort.Strings(portChannels)

		t := cli.NewTable("NAME", "STATUS", "MEMBERS", "ACTIVE")

		for _, pcName := range portChannels {
			pc, err := n.ShowLAGDetail(pcName)
			if err != nil {
				continue
			}

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

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag show PortChannel100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]

		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		pc, err := n.ShowLAGDetail(lagName)
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

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		n, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer n.Close()

		statuses, err := n.LAGStatus()
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

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4
  newtron -d leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4 --fast-rate -x`,
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

		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGCreate, authCtx); err != nil {
				return err
			}
			if err := n.CreatePortChannel(ctx, lagName, newtron.PortChannelConfig{
				Members:  members,
				MinLinks: lagMinLinks,
				FastRate: lagFastRate,
				MTU:      lagMTU,
			}); err != nil {
				return fmt.Errorf("creating LAG: %w", err)
			}
			return nil
		})
	},
}

var lagAddInterfaceCmd = &cobra.Command{
	Use:   "add-interface <lag-name> <interface>",
	Short: "Add an interface to a LAG",
	Long: `Add a member interface to a LAG.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag add-interface PortChannel100 Ethernet8`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		memberName := args[1]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
				return err
			}
			if err := n.AddPortChannelMember(ctx, lagName, memberName); err != nil {
				return fmt.Errorf("adding interface: %w", err)
			}
			return nil
		})
	},
}

var lagRemoveInterfaceCmd = &cobra.Command{
	Use:   "remove-interface <lag-name> <interface>",
	Short: "Remove an interface from a LAG",
	Long: `Remove a member interface from a LAG.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag remove-interface PortChannel100 Ethernet8`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		memberName := args[1]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
				return err
			}
			if err := n.RemovePortChannelMember(ctx, lagName, memberName); err != nil {
				return fmt.Errorf("removing interface: %w", err)
			}
			return nil
		})
	},
}

var lagDeleteCmd = &cobra.Command{
	Use:   "delete <lag-name>",
	Short: "Delete a LAG",
	Long: `Delete a link aggregation group.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag delete PortChannel100 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		return withDeviceWrite(func(ctx context.Context, n *newtron.Node) error {
			authCtx := auth.NewContext().WithDevice(app.deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGDelete, authCtx); err != nil {
				return err
			}
			if err := n.DeletePortChannel(ctx, lagName); err != nil {
				return fmt.Errorf("deleting LAG: %w", err)
			}
			return nil
		})
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
