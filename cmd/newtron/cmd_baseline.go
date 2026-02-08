package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/auth"
	"github.com/newtron-network/newtron/pkg/configlet"
	"github.com/newtron-network/newtron/pkg/network"
)

var baselineCmd = &cobra.Command{
	Use:   "baseline",
	Short: "Manage baseline configurations",
	Long: `Manage baseline configuration templates.

Baselines are pre-built configuration templates that can be
applied to devices for day-1 provisioning.

The 'apply' command requires -d (device) flag.

Examples:
  newtron baseline list
  newtron baseline show sonic-baseline
  newtron -d leaf1-ny baseline apply sonic-baseline sonic-qos-8q -x`,
}

func getConfigletDir() string {
	// Try multiple locations
	locations := []string{
		specDir + "/../configlets",
		"./configlets",
		"/etc/newtron/configlets",
	}

	for _, loc := range locations {
		if info, err := os.Stat(loc); err == nil && info.IsDir() {
			return loc
		}
	}
	return locations[0]
}

var baselineListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available baseline templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		configletDir := getConfigletDir()
		names, err := configlet.ListConfiglets(configletDir)
		if err != nil {
			fmt.Println("No configlets found")
			fmt.Printf("(checked: %s)\n", configletDir)
			return nil
		}

		fmt.Println("Available baseline templates:")
		for _, name := range names {
			c, err := configlet.LoadConfiglet(configletDir, name)
			if err != nil {
				fmt.Printf("  %s\n", name)
			} else {
				fmt.Printf("  %s - %s\n", name, c.Description)
			}
		}

		return nil
	},
}

var baselineShowCmd = &cobra.Command{
	Use:   "show <template>",
	Short: "Show baseline template details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		templateName := args[0]

		c, err := configlet.LoadConfiglet(getConfigletDir(), templateName)
		if err != nil {
			return err
		}

		fmt.Printf("Template: %s\n", c.Name)
		fmt.Printf("Description: %s\n", c.Description)
		fmt.Printf("Version: %s\n", c.Version)

		if len(c.Variables) > 0 {
			fmt.Printf("Variables: %v\n", c.Variables)
		}

		fmt.Println("\nTables modified:")
		for table := range c.ConfigDB {
			fmt.Printf("  - %s\n", table)
		}

		return nil
	},
}

var baselineApplyCmd = &cobra.Command{
	Use:   "apply <template>...",
	Short: "Apply baseline template(s) to the device",
	Long: `Apply baseline template(s) to a device.

Requires -d (device) flag.

Examples:
  newtron -d leaf1-ny baseline apply sonic-baseline
  newtron -d leaf1-ny baseline apply sonic-baseline sonic-qos-8q -x`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		templates := args

		authCtx := auth.NewContext().WithDevice(deviceName).WithResource("baseline")
		if err := checkExecutePermission(auth.PermBaselineApply, authCtx); err != nil {
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

		// Build variables map from device profile
		vars := map[string]string{
			"device_name": dev.Name(),
			"loopback_ip": dev.LoopbackIP(),
			"mgmt_ip":     dev.MgmtIP(),
			"router_id":   dev.RouterID(),
			"as_number":   fmt.Sprintf("%d", dev.ASNumber()),
			"region":      dev.Region(),
			"site":        dev.Site(),
		}

		// Load and merge all configlets
		allChanges := network.NewChangeSet(dev.Name(), "baseline.apply")

		configletDir := getConfigletDir()
		for _, templateName := range templates {
			c, err := configlet.LoadConfiglet(configletDir, templateName)
			if err != nil {
				return fmt.Errorf("loading template %s: %w", templateName, err)
			}

			changes, err := configletToChangeSet(c, vars, dev)
			if err != nil {
				return fmt.Errorf("processing template %s: %w", templateName, err)
			}

			// Merge changes
			for _, c := range changes.Changes {
				allChanges.Changes = append(allChanges.Changes, c)
			}
		}

		fmt.Printf("Applying templates to %s:\n", deviceName)
		for _, t := range templates {
			fmt.Printf("  - %s\n", t)
		}
		fmt.Println()

		fmt.Println("Changes to be applied:")
		fmt.Print(allChanges.String())

		if executeMode {
			if err := allChanges.Apply(dev); err != nil {
				return fmt.Errorf("execution failed: %w", err)
			}
			fmt.Println("\n" + green("Baseline applied successfully."))
		} else {
			printDryRunNotice()
		}

		return nil
	},
}

func configletToChangeSet(c *configlet.Configlet, vars map[string]string, dev *network.Device) (*network.ChangeSet, error) {
	cs := network.NewChangeSet(dev.Name(), "baseline."+c.Name)

	resolved := configlet.ResolveConfiglet(c, vars)
	for table, entries := range resolved {
		for key, fields := range entries {
			changeType := network.ChangeAdd
			if entryExists(dev, table, key) {
				changeType = network.ChangeModify
			}
			cs.Add(table, key, changeType, nil, fields)
		}
	}

	return cs, nil
}

func entryExists(dev *network.Device, table, key string) bool {
	configDB := dev.ConfigDB()
	if configDB == nil {
		return false
	}

	// Check common tables
	switch table {
	case "PORT":
		_, ok := configDB.Port[key]
		return ok
	case "VLAN":
		_, ok := configDB.VLAN[key]
		return ok
	case "VRF":
		_, ok := configDB.VRF[key]
		return ok
	case "INTERFACE":
		_, ok := configDB.Interface[key]
		return ok
	case "LOOPBACK_INTERFACE":
		_, ok := configDB.LoopbackInterface[key]
		return ok
	case "ACL_TABLE":
		_, ok := configDB.ACLTable[key]
		return ok
	default:
		// For other tables, assume it doesn't exist (will be added)
		return false
	}
}

func init() {
	baselineCmd.AddCommand(baselineListCmd)
	baselineCmd.AddCommand(baselineShowCmd)
	baselineCmd.AddCommand(baselineApplyCmd)
}
