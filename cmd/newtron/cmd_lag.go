package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/network"
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
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		portChannels := dev.ListPortChannels()

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(portChannels)
		}

		if len(portChannels) == 0 {
			fmt.Println("No LAGs configured")
			return nil
		}

		sort.Strings(portChannels)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tMEMBERS\tACTIVE")
		fmt.Fprintln(w, "----\t------\t-------\t------")

		for _, pcName := range portChannels {
			pc, err := dev.GetPortChannel(pcName)
			if err != nil {
				continue
			}

			status := pc.AdminStatus
			if status == "up" {
				status = green("up")
			} else {
				status = red("down")
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\n",
				pc.Name,
				status,
				strings.Join(pc.Members, ","),
				len(pc.ActiveMembers),
				len(pc.Members),
			)
		}
		w.Flush()

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
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		pc, err := dev.GetPortChannel(lagName)
		if err != nil {
			return err
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(pc)
		}

		fmt.Printf("LAG: %s\n", bold(pc.Name))

		adminStatus := pc.AdminStatus
		if adminStatus == "up" {
			fmt.Printf("Admin Status: %s\n", green("up"))
		} else {
			fmt.Printf("Admin Status: %s\n", red(adminStatus))
		}

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

		// Show interface details for the LAG
		intf, err := dev.GetInterface(pc.Name)
		if err == nil {
			fmt.Printf("MTU: %d\n", intf.MTU())
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
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		portChannels := dev.ListPortChannels()

		if len(portChannels) == 0 {
			fmt.Println("No LAGs configured")
			return nil
		}

		sort.Strings(portChannels)

		type lagStatus struct {
			Name          string   `json:"name"`
			AdminStatus   string   `json:"admin_status"`
			OperStatus    string   `json:"oper_status,omitempty"`
			Members       []string `json:"members"`
			ActiveMembers []string `json:"active_members"`
			MTU           int      `json:"mtu,omitempty"`
		}

		var statuses []lagStatus
		for _, pcName := range portChannels {
			pc, err := dev.GetPortChannel(pcName)
			if err != nil {
				continue
			}
			s := lagStatus{
				Name:          pc.Name,
				AdminStatus:   pc.AdminStatus,
				Members:       pc.Members,
				ActiveMembers: pc.ActiveMembers,
			}

			// Try to get operational info from interface
			if intf, err := dev.GetInterface(pc.Name); err == nil {
				s.OperStatus = intf.OperStatus()
				s.MTU = intf.MTU()
			}

			statuses = append(statuses, s)
		}

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(statuses)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tADMIN\tOPER\tMEMBERS\tACTIVE\tMTU")
		fmt.Fprintln(w, "----\t-----\t----\t-------\t------\t---")

		for _, s := range statuses {
			admin := s.AdminStatus
			if admin == "up" {
				admin = green("up")
			} else {
				admin = red("down")
			}

			oper := s.OperStatus
			if oper == "up" {
				oper = green("up")
			} else if oper != "" {
				oper = red(oper)
			} else {
				oper = "-"
			}

			mtu := "-"
			if s.MTU > 0 {
				mtu = fmt.Sprintf("%d", s.MTU)
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d/%d\t%s\n",
				s.Name,
				admin,
				oper,
				strings.Join(s.Members, ","),
				len(s.ActiveMembers),
				len(s.Members),
				mtu,
			)
		}
		w.Flush()

		return nil
	},
}

var (
	lagMembers  string
	lagMinLinks int
	lagMode     string
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
  newtron -d leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4 --mode active --fast-rate -x`,
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

		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGCreate, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.CreatePortChannel(ctx, lagName, network.PortChannelConfig{
				Members:  members,
				MinLinks: lagMinLinks,
				FastRate: lagFastRate,
				MTU:      lagMTU,
			})
			if err != nil {
				return nil, fmt.Errorf("creating LAG: %w", err)
			}
			return cs, nil
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.AddPortChannelMember(ctx, lagName, memberName)
			if err != nil {
				return nil, fmt.Errorf("adding interface: %w", err)
			}
			return cs, nil
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.RemovePortChannelMember(ctx, lagName, memberName)
			if err != nil {
				return nil, fmt.Errorf("removing interface: %w", err)
			}
			return cs, nil
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
		return withDeviceWrite(func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error) {
			authCtx := auth.NewContext().WithDevice(deviceName).WithResource(lagName)
			if err := checkExecutePermission(auth.PermLAGCreate, authCtx); err != nil {
				return nil, err
			}
			cs, err := dev.DeletePortChannel(ctx, lagName)
			if err != nil {
				return nil, fmt.Errorf("deleting LAG: %w", err)
			}
			return cs, nil
		})
	},
}

func init() {
	lagCreateCmd.Flags().StringVar(&lagMembers, "members", "", "Comma-separated list of member interfaces (required)")
	lagCreateCmd.Flags().IntVar(&lagMinLinks, "min-links", 1, "Minimum links required")
	lagCreateCmd.Flags().StringVar(&lagMode, "mode", "active", "LACP mode: active, passive, or on")
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
