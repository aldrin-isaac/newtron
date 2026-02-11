package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/settings"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Manage persistent settings",
	Long: `Manage persistent settings stored in ~/.newtron/settings.json.

Settings provide defaults for context flags:
  - default_network: Used when -n is not specified
  - default_device:  Used when -d is not specified
  - spec_dir:        Specification directory

Examples:
  newtron settings show
  newtron settings set network production
  newtron settings set device leaf1-ny
  newtron settings set specs /etc/newtron
  newtron settings clear`,
}

var settingsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := settings.Load()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}

		fmt.Printf("Settings file: %s\n\n", settings.DefaultSettingsPath())

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SETTING\tVALUE")
		fmt.Fprintln(w, "-------\t-----")

		printSetting := func(name, value string) {
			if value == "" {
				value = "(not set)"
			}
			fmt.Fprintf(w, "%s\t%s\n", name, value)
		}

		printSetting("default_network", s.DefaultNetwork)
		printSetting("default_device", s.DefaultDevice)
		printSetting("spec_dir", s.SpecDir)
		printSetting("last_device", s.LastDevice)
		printSetting("lab_specs", s.LabSpecs)
		printSetting("default_suite", s.DefaultSuite)
		printSetting("topologies_dir", s.TopologiesDir)

		w.Flush()
		return nil
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set <setting> <value>",
	Short: "Set a setting value",
	Long: `Set a persistent setting value.

Available settings:
  network        - Default network name (-n flag default)
  device         - Default device name (-d flag default)
  specs          - Specification directory (-s flag default)
  lab_specs      - Default newtlab spec directory (-S flag default)
  suite          - Default newtest suite directory (--dir flag default)
  topologies_dir - Base directory for newtest topologies

Examples:
  newtron settings set network production
  newtron settings set device leaf1-ny
  newtron settings set specs /etc/newtron
  newtron settings set suite newtest/suites/2node-incremental`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		setting := args[0]
		value := args[1]

		s, err := settings.Load()
		if err != nil {
			s = &settings.Settings{}
		}

		switch setting {
		case "network":
			s.DefaultNetwork = value
			fmt.Printf("Default network set to: %s\n", value)
		case "device":
			s.DefaultDevice = value
			fmt.Printf("Default device set to: %s\n", value)
		case "specs", "spec_dir":
			s.SpecDir = value
			fmt.Printf("Specification directory set to: %s\n", value)
		case "lab_specs":
			s.LabSpecs = value
			fmt.Printf("Lab specs directory set to: %s\n", value)
		case "suite", "default_suite":
			s.DefaultSuite = value
			fmt.Printf("Default suite set to: %s\n", value)
		case "topologies_dir":
			s.TopologiesDir = value
			fmt.Printf("Topologies directory set to: %s\n", value)
		default:
			return fmt.Errorf("unknown setting: %s (valid: network, device, specs, lab_specs, suite, topologies_dir)", setting)
		}

		if err := s.Save(); err != nil {
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

		s, err := settings.Load()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}

		var value string
		switch setting {
		case "network":
			value = s.DefaultNetwork
		case "device":
			value = s.DefaultDevice
		case "specs", "spec_dir":
			value = s.SpecDir
		case "last_device":
			value = s.LastDevice
		case "lab_specs":
			value = s.LabSpecs
		case "suite", "default_suite":
			value = s.DefaultSuite
		case "topologies_dir":
			value = s.TopologiesDir
		default:
			return fmt.Errorf("unknown setting: %s", setting)
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
		s := &settings.Settings{}
		if err := s.Save(); err != nil {
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
		fmt.Println(settings.DefaultSettingsPath())
	},
}

func init() {
	settingsCmd.AddCommand(settingsShowCmd)
	settingsCmd.AddCommand(settingsSetCmd)
	settingsCmd.AddCommand(settingsGetCmd)
	settingsCmd.AddCommand(settingsClearCmd)
	settingsCmd.AddCommand(settingsPathCmd)
}
