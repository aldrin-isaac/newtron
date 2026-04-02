package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var sshCmd = &cobra.Command{
	Use:   "ssh <command...>",
	Short: "Execute SSH command on a device",
	Long: `Execute a command on the device via SSH.

The command arguments are joined with spaces and run on the device. Output is
returned as a string. Useful for ad-hoc diagnostics without opening a full
SSH session.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 ssh show ip bgp summary
  newtron -D leaf1 ssh "redis-cli -n 4 HGETALL 'BGP_GLOBALS|default'"
  newtron -D leaf1 ssh show version --json`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		command := strings.Join(args, " ")

		output, err := app.client.SSHCommand(app.deviceName, command)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"output": output})
		}

		fmt.Print(output)
		return nil
	},
}

var reloadConfigCmd = &cobra.Command{
	Use:   "reload-config",
	Short: "Reload CONFIG_DB from config_db.json",
	Long: `Reload the device configuration from config_db.json.

This runs the SONiC config reload command on the device, which restores
CONFIG_DB from the saved config_db.json file. Use this to recover from
drift or to apply a previously saved configuration.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 reload-config`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		if err := app.client.ConfigReload(app.deviceName); err != nil {
			return err
		}

		fmt.Println("Config reloaded.")
		return nil
	},
}

var saveConfigCmd = &cobra.Command{
	Use:   "save-config",
	Short: "Save CONFIG_DB to config_db.json",
	Long: `Save the running CONFIG_DB to config_db.json on the device.

This runs the SONiC config save command, persisting the current CONFIG_DB
state so it survives a reboot. Newtron normally calls this automatically
after write operations unless --no-save is specified.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 save-config`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		if err := app.client.SaveConfig(app.deviceName); err != nil {
			return err
		}

		fmt.Println("Config saved.")
		return nil
	},
}

var restartDaemonCmd = &cobra.Command{
	Use:   "restart-daemon <name>",
	Short: "Restart a SONiC daemon/service",
	Long: `Restart a SONiC daemon or Docker service on the device.

Use this to recover from daemon failures or to apply configuration changes
that require a daemon restart (e.g., BGP ASN changes require restarting bgp).

Requires -D (device) flag.

Examples:
  newtron -D leaf1 restart-daemon bgp
  newtron -D leaf1 restart-daemon swss`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		name := args[0]

		if err := app.client.RestartService(app.deviceName, name); err != nil {
			return err
		}

		fmt.Printf("Restarted %s.\n", name)
		return nil
	},
}
