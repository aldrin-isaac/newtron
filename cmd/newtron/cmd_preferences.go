package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var preferencesCmd = &cobra.Command{
	Use:   "preferences",
	Short: "Manage local CLI preferences",
	Long: `Manage local CLI preferences stored in ~/.newtron/settings.json.

These are per-user defaults for CLI context flags — not network operational
settings. For operational settings (drift_guard, max_history), use 'newtron settings'.

Available keys:
  network        - Default network name (-n flag default)
  specs          - Specification directory (-S flag default)
  suite          - Default newtrun suite directory (--dir flag default)
  topologies_dir - Base directory for newtrun topologies
  server         - newtron-server HTTP address (default: http://localhost:8080)
  network_id     - Network identifier for server operations (default: "default")

Examples:
  newtron preferences show
  newtron preferences set network production
  newtron preferences set specs /etc/newtron
  newtron preferences clear`,
}

var preferencesShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current preferences",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := newtron.LoadSettings()
		if err != nil {
			return fmt.Errorf("loading preferences: %w", err)
		}

		fmt.Printf("Preferences file: %s\n\n", newtron.SettingsPath())

		t := cli.NewTable("KEY", "VALUE")

		printPref := func(name, value string) {
			if value == "" {
				value = "(not set)"
			}
			t.Row(name, value)
		}

		printPref("network", s.DefaultNetwork)
		printPref("specs", s.SpecDir)
		printPref("suite", s.DefaultSuite)
		printPref("topologies_dir", s.TopologiesDir)
		printPref("server", s.ServerURL)
		printPref("network_id", s.NetworkID)

		t.Flush()
		return nil
	},
}

var preferencesSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a preference value",
	Long: `Set a persistent preference value.

Available keys:
  network        - Default network name (-n flag default)
  specs          - Specification directory (-S flag default for newtron and newtlab)
  suite          - Default newtrun suite directory (--dir flag default)
  topologies_dir - Base directory for newtrun topologies
  server         - newtron-server HTTP address (default: http://localhost:8080)
  network_id     - Network identifier for server operations (default: "default")

Examples:
  newtron preferences set network production
  newtron preferences set specs /etc/newtron
  newtron preferences set server http://10.0.0.1:8080
  newtron preferences set network_id lab1`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		s, err := newtron.LoadSettings()
		if err != nil {
			s = &newtron.UserSettings{}
		}

		switch key {
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
		case "server", "server_url":
			s.ServerURL = value
			fmt.Printf("Server URL set to: %s\n", value)
		case "network_id":
			s.NetworkID = value
			fmt.Printf("Network ID set to: %s\n", value)
		default:
			return fmt.Errorf("unknown key: %s (valid: network, specs, suite, topologies_dir, server, network_id)", key)
		}

		if err := newtron.SaveSettings(s); err != nil {
			return fmt.Errorf("saving preferences: %w", err)
		}

		return nil
	},
}

var preferencesGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a preference value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]

		s, err := newtron.LoadSettings()
		if err != nil {
			return fmt.Errorf("loading preferences: %w", err)
		}

		var value string
		switch key {
		case "network":
			value = s.DefaultNetwork
		case "specs", "spec_dir":
			value = s.SpecDir
		case "suite", "default_suite":
			value = s.DefaultSuite
		case "topologies_dir":
			value = s.TopologiesDir
		case "server", "server_url":
			value = s.ServerURL
		case "network_id":
			value = s.NetworkID
		default:
			return fmt.Errorf("unknown key: %s (valid: network, specs, suite, topologies_dir, server, network_id)", key)
		}

		if value == "" {
			fmt.Println("(not set)")
		} else {
			fmt.Println(value)
		}
		return nil
	},
}

var preferencesClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear all preferences",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := &newtron.UserSettings{}
		if err := newtron.SaveSettings(s); err != nil {
			return fmt.Errorf("saving preferences: %w", err)
		}
		fmt.Println("All preferences cleared.")
		return nil
	},
}

var preferencesPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show preferences file path",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(newtron.SettingsPath())
		return nil
	},
}

func init() {
	preferencesCmd.AddCommand(preferencesShowCmd)
	preferencesCmd.AddCommand(preferencesSetCmd)
	preferencesCmd.AddCommand(preferencesGetCmd)
	preferencesCmd.AddCommand(preferencesClearCmd)
	preferencesCmd.AddCommand(preferencesPathCmd)
}
