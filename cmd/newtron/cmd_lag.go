package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
  newtron -d leaf1-ny lag create PortChannel100 --members Ethernet0,Ethernet4
  newtron -d leaf1-ny lag add-member PortChannel100 Ethernet8
  newtron -d leaf1-ny lag remove-member PortChannel100 Ethernet8`,
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

		// Check permissions
		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(lagName)
		if err := checkExecutePermission(auth.PermLAGCreate, authCtx); err != nil {
			return err
		}

		if lagMembers == "" {
			return fmt.Errorf("--members is required")
		}
		members := strings.Split(lagMembers, ",")
		for i := range members {
			members[i] = strings.TrimSpace(members[i])
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Create LAG using OO style (method on device)
		changeSet, err := dev.CreatePortChannel(ctx, lagName, network.PortChannelConfig{
			Members:  members,
			MinLinks: lagMinLinks,
			FastRate: lagFastRate,
			MTU:      lagMTU,
		})
		if err != nil {
			return fmt.Errorf("creating LAG: %w", err)
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

var lagAddMemberCmd = &cobra.Command{
	Use:   "add-member <lag-name> <interface>",
	Short: "Add a member to a LAG",
	Long: `Add a member interface to a LAG.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag add-member PortChannel100 Ethernet8`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		memberName := args[1]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(lagName)
		if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
			return err
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Add member using OO style (method on device)
		changeSet, err := dev.AddPortChannelMember(ctx, lagName, memberName)
		if err != nil {
			return fmt.Errorf("adding member: %w", err)
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

var lagRemoveMemberCmd = &cobra.Command{
	Use:   "remove-member <lag-name> <interface>",
	Short: "Remove a member from a LAG",
	Long: `Remove a member interface from a LAG.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny lag remove-member PortChannel100 Ethernet8`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		lagName := args[0]
		memberName := args[1]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(lagName)
		if err := checkExecutePermission(auth.PermLAGModify, authCtx); err != nil {
			return err
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if err := dev.Lock(); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		// Remove member using OO style (method on device)
		changeSet, err := dev.RemovePortChannelMember(ctx, lagName, memberName)
		if err != nil {
			return fmt.Errorf("removing member: %w", err)
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

func init() {
	lagCreateCmd.Flags().StringVar(&lagMembers, "members", "", "Comma-separated list of member interfaces (required)")
	lagCreateCmd.Flags().IntVar(&lagMinLinks, "min-links", 1, "Minimum links required")
	lagCreateCmd.Flags().StringVar(&lagMode, "mode", "active", "LACP mode: active, passive, or on")
	lagCreateCmd.Flags().BoolVar(&lagFastRate, "fast-rate", true, "Use LACP fast rate (1s vs 30s)")
	lagCreateCmd.Flags().IntVar(&lagMTU, "mtu", 9100, "MTU size")

	lagCmd.AddCommand(lagListCmd)
	lagCmd.AddCommand(lagCreateCmd)
	lagCmd.AddCommand(lagAddMemberCmd)
	lagCmd.AddCommand(lagRemoveMemberCmd)
}
