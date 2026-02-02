package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/network"
)

var evpnCmd = &cobra.Command{
	Use:   "evpn",
	Short: "Manage EVPN/VXLAN configuration",
	Long: `Manage EVPN and VXLAN configuration.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny evpn list
  newtron -d leaf1-ny evpn show Vrf_CUST1
  newtron -d leaf1-ny evpn create-vtep --source-ip 10.0.0.10
  newtron -d leaf1-ny evpn create-vrf Vrf_CUST1 --l3vni 10001
  newtron -d leaf1-ny evpn map-l2vni 100 1100  # Map VLAN 100 to VNI 1100
  newtron -d leaf1-ny evpn unmap-l2vni 100     # Unmap L2VNI from VLAN 100
  newtron -d leaf1-ny evpn map-l3vni Vrf_CUST1 10001`,
}

var evpnListCmd = &cobra.Command{
	Use:   "list",
	Short: "List EVPN configuration",
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

		// Show VTEP
		fmt.Println("VTEP Configuration:")
		if len(configDB.VXLANTunnel) == 0 {
			fmt.Println("  (not configured)")
		}
		for name, vtep := range configDB.VXLANTunnel {
			fmt.Printf("  %s: source_ip=%s\n", name, vtep.SrcIP)
		}

		// Show NVO
		fmt.Println("\nEVPN NVO:")
		if len(configDB.VXLANEVPNNVO) == 0 {
			fmt.Println("  (not configured)")
		}
		for name, nvo := range configDB.VXLANEVPNNVO {
			fmt.Printf("  %s: source_vtep=%s\n", name, nvo.SourceVTEP)
		}

		// Show VNI mappings
		fmt.Println("\nVNI Mappings:")
		if len(configDB.VXLANTunnelMap) == 0 {
			fmt.Println("  (none)")
		} else {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  VNI\tTYPE\tRESOURCE")
			fmt.Fprintln(w, "  ---\t----\t--------")
			for _, mapping := range configDB.VXLANTunnelMap {
				resType := "L2"
				res := mapping.VLAN
				if mapping.VRF != "" {
					resType = "L3"
					res = mapping.VRF
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\n", mapping.VNI, resType, res)
			}
			w.Flush()
		}

		// Show VRFs with VNI
		fmt.Println("\nVRFs with L3VNI:")
		hasVRFs := false
		for _, vrfName := range dev.ListVRFs() {
			vrf, _ := dev.GetVRF(vrfName)
			if vrf != nil && vrf.L3VNI > 0 {
				fmt.Printf("  %s: L3VNI=%d\n", vrfName, vrf.L3VNI)
				hasVRFs = true
			}
		}
		if !hasVRFs {
			fmt.Println("  (none)")
		}

		return nil
	},
}

var evpnShowCmd = &cobra.Command{
	Use:   "show <vrf-name>",
	Short: "Show EVPN VRF details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		vrf, err := dev.GetVRF(vrfName)
		if err != nil {
			return err
		}

		fmt.Printf("VRF: %s\n", vrf.Name)
		fmt.Printf("  L3VNI: %d\n", vrf.L3VNI)
		fmt.Printf("  Interfaces: %v\n", vrf.Interfaces)

		return nil
	},
}

var vtepSourceIP string

var evpnCreateVTEPCmd = &cobra.Command{
	Use:   "create-vtep",
	Short: "Create VXLAN Tunnel Endpoint (VTEP)",
	Long: `Create a VXLAN Tunnel Endpoint (VTEP).

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny evpn create-vtep --source-ip 10.0.0.10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		authCtx := auth.NewContext().WithDevice(deviceName).WithResource("vtep")
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
			return err
		}

		if vtepSourceIP == "" {
			return fmt.Errorf("--source-ip is required")
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

		changeSet, err := dev.CreateVTEP(ctx, network.VTEPConfig{SourceIP: vtepSourceIP})
		if err != nil {
			return fmt.Errorf("creating VTEP: %w", err)
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

var l3vni int

var evpnCreateVRFCmd = &cobra.Command{
	Use:   "create-vrf <vrf-name>",
	Short: "Create VRF with L3VNI for EVPN",
	Long: `Create a VRF with L3VNI for EVPN.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny evpn create-vrf Vrf_CUST1 --l3vni 10001`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(vrfName)
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
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

		changeSet, err := dev.CreateVRF(ctx, vrfName, network.VRFConfig{
			L3VNI: l3vni,
		})
		if err != nil {
			return fmt.Errorf("creating VRF: %w", err)
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

var evpnDeleteVRFCmd = &cobra.Command{
	Use:   "delete-vrf <vrf-name>",
	Short: "Delete VRF",
	Long: `Delete a VRF.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny evpn delete-vrf Vrf_CUST1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(vrfName)
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
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

		changeSet, err := dev.DeleteVRF(ctx, vrfName)
		if err != nil {
			return fmt.Errorf("deleting VRF: %w", err)
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

var evpnMapL2VNICmd = &cobra.Command{
	Use:   "map-l2vni <vlan-id> <vni>",
	Short: "Map VLAN to L2VNI",
	Long: `Map a VLAN to an L2VNI for VXLAN.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny evpn map-l2vni 100 1100`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var vlanID, vni int
		if _, err := fmt.Sscanf(args[0], "%d", &vlanID); err != nil {
			return fmt.Errorf("invalid VLAN ID: %s", args[0])
		}
		if _, err := fmt.Sscanf(args[1], "%d", &vni); err != nil {
			return fmt.Errorf("invalid VNI: %s", args[1])
		}

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
			return err
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !dev.VLANExists(vlanID) {
			return fmt.Errorf("VLAN %d does not exist", vlanID)
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.MapL2VNI(ctx, vlanID, vni)
		if err != nil {
			return fmt.Errorf("mapping L2VNI: %w", err)
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

var evpnMapL3VNICmd = &cobra.Command{
	Use:   "map-l3vni <vrf-name> <vni>",
	Short: "Map VRF to L3VNI",
	Long: `Map a VRF to an L3VNI for VXLAN.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny evpn map-l3vni Vrf_CUST1 10001`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vrfName := args[0]
		var vni int
		if _, err := fmt.Sscanf(args[1], "%d", &vni); err != nil {
			return fmt.Errorf("invalid VNI: %s", args[1])
		}

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(vrfName)
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
			return err
		}

		ctx := context.Background()
		dev, err := requireDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		if !dev.VRFExists(vrfName) {
			return fmt.Errorf("VRF %s does not exist", vrfName)
		}

		if err := dev.Lock(ctx); err != nil {
			return fmt.Errorf("locking device: %w", err)
		}
		defer dev.Unlock()

		changeSet, err := dev.MapL3VNI(ctx, vrfName, vni)
		if err != nil {
			return fmt.Errorf("mapping L3VNI: %w", err)
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

var evpnUnmapL2VNICmd = &cobra.Command{
	Use:   "unmap-l2vni <vlan-id>",
	Short: "Unmap L2VNI from a VLAN",
	Long: `Remove the L2VNI mapping from a VLAN.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny evpn unmap-l2vni 100`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var vlanID int
		if _, err := fmt.Sscanf(args[0], "%d", &vlanID); err != nil {
			return fmt.Errorf("invalid VLAN ID: %s", args[0])
		}

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource(fmt.Sprintf("Vlan%d", vlanID))
		if err := checkExecutePermission(auth.PermEVPNModify, authCtx); err != nil {
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

		changeSet, err := dev.UnmapL2VNI(ctx, vlanID)
		if err != nil {
			return fmt.Errorf("unmapping L2VNI: %w", err)
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
	evpnCreateVTEPCmd.Flags().StringVar(&vtepSourceIP, "source-ip", "", "Source IP address for VTEP (typically loopback IP)")

	evpnCreateVRFCmd.Flags().IntVar(&l3vni, "l3vni", 0, "L3VNI for the VRF")

	evpnCmd.AddCommand(evpnListCmd)
	evpnCmd.AddCommand(evpnShowCmd)
	evpnCmd.AddCommand(evpnCreateVTEPCmd)
	evpnCmd.AddCommand(evpnCreateVRFCmd)
	evpnCmd.AddCommand(evpnDeleteVRFCmd)
	evpnCmd.AddCommand(evpnMapL2VNICmd)
	evpnCmd.AddCommand(evpnUnmapL2VNICmd)
	evpnCmd.AddCommand(evpnMapL3VNICmd)
}
