package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var (
	profileCreateMgmtIP      string
	profileCreateLoopbackIP  string
	profileCreateZone        string
	profileCreatePlatform    string
	profileCreateUnderlayASN int
	profileCreateSSHUser     string
	profileCreateSSHPass     string
	profileCreateSSHPort     int
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage device profiles",
	Long: `Manage device profiles.

Profiles define per-device settings including management IP, loopback IP,
zone membership, platform, and EVPN peering configuration.

Examples:
  newtron profile list
  newtron profile show switch1
  newtron profile create switch3 --mgmt-ip 10.0.0.3 --loopback-ip 10.0.0.3 --zone dc1 -x
  newtron profile delete switch3 -x`,
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all device profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		profiles, err := app.client.ListProfiles()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(profiles)
		}

		if len(profiles) == 0 {
			fmt.Println("No profiles defined")
			return nil
		}

		t := cli.NewTable("NAME", "MGMT IP", "LOOPBACK", "ZONE", "PLATFORM", "ASN")
		for _, name := range profiles {
			p, _ := app.client.ShowProfile(name)
			if p != nil {
				asn := ""
				if p.UnderlayASN > 0 {
					asn = fmt.Sprintf("%d", p.UnderlayASN)
				}
				t.Row(name, p.MgmtIP, p.LoopbackIP, p.Zone, p.Platform, asn)
			}
		}
		t.Flush()

		return nil
	},
}

var profileShowCmd = &cobra.Command{
	Use:   "show <profile-name>",
	Short: "Show device profile details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		p, err := app.client.ShowProfile(name)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(p)
		}

		fmt.Printf("Profile: %s\n", bold(name))
		fmt.Printf("Management IP: %s\n", p.MgmtIP)
		fmt.Printf("Loopback IP: %s\n", p.LoopbackIP)
		fmt.Printf("Zone: %s\n", p.Zone)
		if p.Platform != "" {
			fmt.Printf("Platform: %s\n", p.Platform)
		}
		if p.UnderlayASN > 0 {
			fmt.Printf("Underlay ASN: %d\n", p.UnderlayASN)
		}
		if p.MAC != "" {
			fmt.Printf("MAC: %s\n", p.MAC)
		}
		if p.SSHUser != "" {
			fmt.Printf("SSH User: %s\n", p.SSHUser)
		}
		if p.SSHPort > 0 {
			fmt.Printf("SSH Port: %d\n", p.SSHPort)
		}
		if p.EVPN != nil {
			fmt.Println("\nEVPN Peering:")
			if len(p.EVPN.Peers) > 0 {
				fmt.Printf("  Peers: %v\n", p.EVPN.Peers)
			}
			if p.EVPN.RouteReflector {
				fmt.Println("  Route Reflector: yes")
			}
			if p.EVPN.ClusterID != "" {
				fmt.Printf("  Cluster ID: %s\n", p.EVPN.ClusterID)
			}
		}

		return nil
	},
}

var profileCreateCmd = &cobra.Command{
	Use:   "create <profile-name>",
	Short: "Create a new device profile",
	Long: `Create a new device profile.

This is a spec-level command (no device needed).

Flags:
  --mgmt-ip       Management IP address (required)
  --loopback-ip   Loopback IP address
  --zone          Zone name (required)
  --platform      Platform name
  --underlay-asn  BGP underlay AS number
  --ssh-user      SSH username
  --ssh-pass      SSH password
  --ssh-port      SSH port (default 22)

Examples:
  newtron profile create switch3 --mgmt-ip 10.0.0.3 --loopback-ip 10.0.0.3 --zone dc1 --platform ciscovs -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if profileCreateMgmtIP == "" {
			return fmt.Errorf("--mgmt-ip is required")
		}
		if profileCreateZone == "" {
			return fmt.Errorf("--zone is required")
		}

		if err := app.client.CreateProfile(newtron.CreateDeviceProfileRequest{
			Name:        name,
			MgmtIP:      profileCreateMgmtIP,
			LoopbackIP:  profileCreateLoopbackIP,
			Zone:        profileCreateZone,
			Platform:    profileCreatePlatform,
			UnderlayASN: profileCreateUnderlayASN,
			SSHUser:     profileCreateSSHUser,
			SSHPass:     profileCreateSSHPass,
			SSHPort:     profileCreateSSHPort,
		}, execOpts()); err != nil {
			return err
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Created profile '%s'\n", name)
		return nil
	},
}

var profileDeleteCmd = &cobra.Command{
	Use:   "delete <profile-name>",
	Short: "Delete a device profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if err := app.client.DeleteProfile(name, execOpts()); err != nil {
			return err
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Deleted profile '%s'\n", name)
		return nil
	},
}

func init() {
	profileCreateCmd.Flags().StringVar(&profileCreateMgmtIP, "mgmt-ip", "", "Management IP address (required)")
	profileCreateCmd.Flags().StringVar(&profileCreateLoopbackIP, "loopback-ip", "", "Loopback IP address")
	profileCreateCmd.Flags().StringVar(&profileCreateZone, "zone", "", "Zone name (required)")
	profileCreateCmd.Flags().StringVar(&profileCreatePlatform, "platform", "", "Platform name")
	profileCreateCmd.Flags().IntVar(&profileCreateUnderlayASN, "underlay-asn", 0, "BGP underlay AS number")
	profileCreateCmd.Flags().StringVar(&profileCreateSSHUser, "ssh-user", "", "SSH username")
	profileCreateCmd.Flags().StringVar(&profileCreateSSHPass, "ssh-pass", "", "SSH password")
	profileCreateCmd.Flags().IntVar(&profileCreateSSHPort, "ssh-port", 0, "SSH port")

	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileShowCmd)
	profileCmd.AddCommand(profileCreateCmd)
	profileCmd.AddCommand(profileDeleteCmd)
}
