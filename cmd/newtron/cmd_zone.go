package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
)

var zoneCmd = &cobra.Command{
	Use:   "zone",
	Short: "Manage network zones",
	Long: `Manage network zones.

Zones group devices by location or function and can carry zone-level
spec overrides (services, filters, etc.).

Examples:
  newtron zone list
  newtron zone show dc1
  newtron zone create dc2 -x
  newtron zone delete dc2 -x`,
}

var zoneListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all zones",
	RunE: func(cmd *cobra.Command, args []string) error {
		zones, err := app.client.ListZones()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(zones)
		}

		if len(zones) == 0 {
			fmt.Println("No zones defined")
			return nil
		}

		for _, name := range zones {
			fmt.Println(name)
		}

		return nil
	},
}

var zoneShowCmd = &cobra.Command{
	Use:   "show <zone-name>",
	Short: "Show zone details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		z, err := app.client.ShowZone(name)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(z)
		}

		fmt.Printf("Zone: %s\n", bold(name))
		return nil
	},
}

var zoneCreateCmd = &cobra.Command{
	Use:   "create <zone-name>",
	Short: "Create a new zone",
	Long: `Create a new zone in network.json.

This is a spec-level command (no device needed).

Examples:
  newtron zone create dc2 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if err := app.client.CreateZone(newtron.CreateZoneRequest{
			Name: name,
		}, execOpts()); err != nil {
			return err
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Created zone '%s'\n", name)
		return nil
	},
}

var zoneDeleteCmd = &cobra.Command{
	Use:   "delete <zone-name>",
	Short: "Delete a zone",
	Long: `Delete a zone from network.json.

Returns error if any device profile references this zone.

Examples:
  newtron zone delete dc2 -x`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if err := app.client.DeleteZone(name, execOpts()); err != nil {
			return err
		}

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		fmt.Printf("Deleted zone '%s'\n", name)
		return nil
	},
}

func init() {
	zoneCmd.AddCommand(zoneListCmd)
	zoneCmd.AddCommand(zoneShowCmd)
	zoneCmd.AddCommand(zoneCreateCmd)
	zoneCmd.AddCommand(zoneDeleteCmd)
}
