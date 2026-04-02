package main

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
)

var statedbCmd = &cobra.Command{
	Use:   "statedb",
	Short: "Query STATE_DB on a device",
	Long: `Query STATE_DB entries on a SONiC device.

STATE_DB holds operational state written by SONiC daemons — port link state,
BGP peer state, FDB entries, and more. Use this for diagnostics when you need
to inspect raw operational state that hasn't been surfaced by higher-level
commands.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 statedb query PORT_TABLE Ethernet0
  newtron -D leaf1 statedb query BGP_PEER_TABLE 10.0.0.2`,
}

var statedbQueryCmd = &cobra.Command{
	Use:   "query <table> <key>",
	Short: "Query a STATE_DB entry",
	Long: `Read all fields of a STATE_DB hash entry on the device.

Prints each field and its value, sorted alphabetically by field name.

Requires -D (device) flag.

Examples:
  newtron -D leaf1 statedb query PORT_TABLE Ethernet0
  newtron -D leaf1 statedb query VLAN_TABLE Vlan100 --json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		table, key := args[0], args[1]

		fields, err := app.client.QueryStateDB(app.deviceName, table, key)
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

func init() {
	statedbCmd.AddCommand(statedbQueryCmd)
}
