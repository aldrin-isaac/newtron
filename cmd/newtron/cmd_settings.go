package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Manage persistent settings",
	Long: `Manage persistent settings stored in ~/.newtron/settings.json.

Settings provide defaults for context flags:
  - default_network: Used when -n is not specified
  - spec_dir:        Specification directory

Examples:
  newtron settings show
  newtron settings set network production
  newtron settings set specs /etc/newtron
  newtron settings clear`,
}

var settingsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newtron.LoadSettings()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}

		fmt.Printf("Settings file: %s\n\n", newtron.SettingsPath())

		t := cli.NewTable("SETTING", "VALUE")

		printSetting := func(name, value string) {
			if value == "" {
				value = "(not set)"
			}
			t.Row(name, value)
		}

		printSetting("default_network", s.DefaultNetwork)
		printSetting("spec_dir", s.SpecDir)
		printSetting("default_suite", s.DefaultSuite)
		printSetting("topologies_dir", s.TopologiesDir)

		t.Flush()
		return nil
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set <setting> <value>",
	Short: "Set a setting value",
	Long: `Set a persistent setting value.

Available settings:
  network        - Default network name (-n flag default)
  specs          - Specification directory (-S flag default for newtron and newtlab)
  suite          - Default newtrun suite directory (--dir flag default)
  topologies_dir - Base directory for newtrun topologies

Examples:
  newtron settings set network production
  newtron settings set specs /etc/newtron
  newtron settings set suite newtrun/suites/2node-incremental`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		setting := args[0]
		value := args[1]

		s, err := newtron.LoadSettings()
		if err != nil {
			s = &newtron.UserSettings{}
		}

		switch setting {
		case "network":
			s.DefaultNetwork = value
			fmt.Printf("Default network set to: %s\n", value)
		case "specs", "spec_dir":
			s.SpecDir = value
			fmt.Printf("Specification directory set to: %s\n", value)
		case "suite", "default_suite":
			s.DefaultSuite = value
			fmt.Printf("Default suite set to: %s\n", value)
		case "topologies_dir":
			s.TopologiesDir = value
			fmt.Printf("Topologies directory set to: %s\n", value)
		default:
			return fmt.Errorf("unknown setting: %s (valid: network, specs, suite, topologies_dir)", setting)
		}

		if err := newtron.SaveSettings(s); err != nil {
			return fmt.Errorf("saving settings: %w", err)
		}

		return nil
	},
}

var settingsGetCmd = &cobra.Command{
	Use:   "get <setting>",
	Short: "Get a setting value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		setting := args[0]

		s, err := newtron.LoadSettings()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}

		var value string
		switch setting {
		case "network":
			value = s.DefaultNetwork
		case "specs", "spec_dir":
			value = s.SpecDir
		case "suite", "default_suite":
			value = s.DefaultSuite
		case "topologies_dir":
			value = s.TopologiesDir
		default:
			return fmt.Errorf("unknown setting: %s (valid: network, specs, suite, topologies_dir)", setting)
		}

		if value == "" {
			fmt.Println("(not set)")
		} else {
			fmt.Println(value)
		}
		return nil
	},
}

var settingsClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear all settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := &newtron.UserSettings{}
		if err := newtron.SaveSettings(s); err != nil {
			return fmt.Errorf("saving settings: %w", err)
		}
		fmt.Println("All settings cleared.")
		return nil
	},
}

var settingsPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show settings file path",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(newtron.SettingsPath())
	},
}

func init() {
	settingsCmd.AddCommand(settingsShowCmd)
	settingsCmd.AddCommand(settingsSetCmd)
	settingsCmd.AddCommand(settingsGetCmd)
	settingsCmd.AddCommand(settingsClearCmd)
	settingsCmd.AddCommand(settingsPathCmd)
}
