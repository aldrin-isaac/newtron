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

var vlanCmd = &cobra.Command{
	Use:   "vlan",
	Short: "Manage VLANs",
	Long: `Manage VLANs on SONiC devices.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan list
  newtron -d leaf1-ny vlan create 100 --name "Frontend"
  newtron -d leaf1-ny vlan add-member 100 Ethernet0 --tagged`,
}

var vlanListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all VLANs",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		vlanIDs := dev.ListVLANs()

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(vlanIDs)
		}

		if len(vlanIDs) == 0 {
			fmt.Println("No VLANs configured")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "VLAN ID\tL2VNI\tSVI\tPORTS")
		fmt.Fprintln(w, "-------\t-----\t---\t-----")

		for _, id := range vlanIDs {
			vlan, err := dev.GetVLAN(id)
			if err != nil {
				continue
			}

			vni := "-"
			if vlan.L2VNI() > 0 {
				vni = fmt.Sprintf("%d", vlan.L2VNI())
			}

			svi := "-"
			if vlan.SVIStatus != "" {
				svi = vlan.SVIStatus
			}

			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
				vlan.ID,
				vni,
				svi,
				strings.Join(vlan.Ports, ","),
			)
		}
		w.Flush()

		return nil
	},
}

var (
	vlanName        string
	vlanDescription string
)

var vlanCreateCmd = &cobra.Command{
	Use:   "create <vlan-id>",
	Short: "Create a new VLAN",
	Long: `Create a new VLAN on the device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan create 100 --name "Frontend"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var vlanID int
		if _, err := fmt.Sscanf(args[0], "%d", &vlanID); err != nil {
			return fmt.Errorf("invalid VLAN ID: %s", args[0])
		}

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
		if err := checkExecutePermission(auth.PermVLANCreate, authCtx); err != nil {
			return err
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

		// Create VLAN using OO style (method on device)
		changeSet, err := dev.CreateVLAN(ctx, vlanID, network.VLANConfig{
			Description: vlanDescription,
		})
		if err != nil {
			return fmt.Errorf("creating VLAN: %w", err)
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

var vlanTagged bool

var vlanAddMemberCmd = &cobra.Command{
	Use:   "add-member <vlan-id> <port>",
	Short: "Add a port to a VLAN",
	Long: `Add a port to a VLAN.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny vlan add-member 100 Ethernet0 --tagged`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var vlanID int
		if _, err := fmt.Sscanf(args[0], "%d", &vlanID); err != nil {
			return fmt.Errorf("invalid VLAN ID: %s", args[0])
		}
		portName := args[1]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
		if err := checkExecutePermission(auth.PermVLANModify, authCtx); err != nil {
			return err
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

		// Add VLAN member using OO style (method on device)
		changeSet, err := dev.AddVLANMember(ctx, vlanID, portName, vlanTagged)
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

// getL2VniCmd retrieves L2VNI mapping for a VLAN
var getL2VniCmd = &cobra.Command{
	Use:   "get-l2vni",
	Short: "Get L2VNI mapping for the selected VLAN",
	Long: `Get L2VNI mapping for the selected VLAN interface.

Requires -d (device) and -i (VLAN) flags.

Examples:
  newtron -d leaf1-ny -i Vlan100 get-l2vni`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dev, intf, err := requireInterface(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !intf.IsVLAN() {
			return fmt.Errorf("get-l2vni requires a VLAN interface (selected: %s)", interfaceName)
		}

		// Get MAC-VPN info which contains L2VNI
		macvpnInfo := intf.MACVPNInfo()

		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(macvpnInfo)
		}

		if macvpnInfo == nil || macvpnInfo.L2VNI == 0 {
			fmt.Printf("No L2VNI mapping configured for %s\n", interfaceName)
			return nil
		}

		fmt.Printf("VLAN: %s\n", interfaceName)
		fmt.Printf("L2VNI: %d\n", macvpnInfo.L2VNI)
		if macvpnInfo.Name != "" {
			fmt.Printf("MAC-VPN: %s\n", macvpnInfo.Name)
		}
		fmt.Printf("ARP Suppression: %v\n", macvpnInfo.ARPSuppression)

		return nil
	},
}

func init() {
	vlanCreateCmd.Flags().StringVar(&vlanName, "name", "", "VLAN name")
	vlanCreateCmd.Flags().StringVar(&vlanDescription, "description", "", "VLAN description")

	vlanAddMemberCmd.Flags().BoolVar(&vlanTagged, "tagged", false, "Add as tagged member")

	vlanCmd.AddCommand(vlanListCmd)
	vlanCmd.AddCommand(vlanCreateCmd)
	vlanCmd.AddCommand(vlanAddMemberCmd)
}
