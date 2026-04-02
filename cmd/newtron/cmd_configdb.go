package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
)

var configdbCmd = &cobra.Command{
	Use:   "configdb",
	Short: "Query CONFIG_DB on a device",
	Long: `Query CONFIG_DB tables and entries on a SONiC device.

Useful for diagnostics — inspect raw CONFIG_DB state without going through
the newtron abstraction layer.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 configdb keys VLAN
  newtron -D leaf1 configdb query BGP_GLOBALS default
  newtron -D leaf1 configdb exists INTERFACE Ethernet0`,
}

var configdbKeysCmd = &cobra.Command{
	Use:   "keys <table>",
	Short: "List keys in a CONFIG_DB table",
	Long: `List all keys present in a CONFIG_DB table on the device.

Returns the raw Redis key names within the table (without the table prefix).

Requires -D (device) flag.

Examples:
  newtron -D leaf1 configdb keys VLAN
  newtron -D leaf1 configdb keys INTERFACE --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		table := args[0]

		keys, err := app.client.ConfigDBTableKeys(app.deviceName, table)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(keys)
		}

		sort.Strings(keys)
		for _, k := range keys {
			fmt.Println(k)
		}

		return nil
	},
}

var configdbQueryCmd = &cobra.Command{
	Use:   "query <table> <key>",
	Short: "Query a CONFIG_DB entry",
	Long: `Read all fields of a CONFIG_DB hash entry on the device.

Prints each field and its value, sorted alphabetically by field name.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 configdb query BGP_GLOBALS default
  newtron -D leaf1 configdb query VLAN Vlan100 --json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		table, key := args[0], args[1]

		fields, err := app.client.QueryConfigDB(app.deviceName, table, key)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(fields)
		}

		names := make([]string, 0, len(fields))
		for f := range fields {
			names = append(names, f)
		}
		sort.Strings(names)

		t := cli.NewTable("FIELD", "VALUE")
		for _, f := range names {
			t.Row(f, fields[f])
		}
		t.Flush()

		return nil
	},
}

var configdbExistsCmd = &cobra.Command{
	Use:   "exists <table> <key>",
	Short: "Check if a CONFIG_DB entry exists",
	Long: `Check whether a CONFIG_DB entry exists on the device.

Exits with output "exists" or "not found". Useful in scripts to check
for the presence of a CONFIG_DB key before querying it.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 configdb exists VLAN Vlan100
  newtron -D leaf1 configdb exists INTERFACE Ethernet0 --json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		table, key := args[0], args[1]

		exists, err := app.client.ConfigDBEntryExists(app.deviceName, table, key)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]bool{"exists": exists})
		}

		if exists {
			fmt.Println("exists")
		} else {
			fmt.Println("not found")
		}

		return nil
	},
}

func init() {
	configdbCmd.AddCommand(configdbKeysCmd)
	configdbCmd.AddCommand(configdbQueryCmd)
	configdbCmd.AddCommand(configdbExistsCmd)
}
