package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/cli"
)

var dbCmd = &cobra.Command{
	Use:   "db <DB> [table] [key]",
	Short: "Read an operational DB on a device",
	Long: `Read a device's operational Redis DBs — STATE_DB, APPL_DB, COUNTERS_DB,
ASIC_DB — raw, as the daemons wrote them.

This is the diagnostic substrate: port oper state (STATE_DB PORT_TABLE),
ARP/neighbor resolution (APPL_DB NEIGH_TABLE), interface counters and rates
(COUNTERS_DB), SAI objects (ASIC_DB). CONFIG_DB has its own command
('configdb') with config-specific semantics.

With one argument, lists the DB's tables and entry counts. With a table,
lists the table's entry keys. With a key, prints the entry's fields. The key
may embed the DB's separator (e.g. APPL_DB 'NEIGH_TABLE Ethernet4:10.0.0.1').

Requires -D (device) flag.

Examples:
  newtron -D leaf1 db STATE_DB
  newtron -D leaf1 db STATE_DB PORT_TABLE
  newtron -D leaf1 db STATE_DB PORT_TABLE Ethernet0
  newtron -D leaf1 db APPL_DB NEIGH_TABLE Ethernet4:10.255.255.4
  newtron -D leaf1 db COUNTERS_DB COUNTERS_PORT_NAME_MAP --json`,
	Args: cobra.RangeArgs(1, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}
		dbName := args[0]

		switch len(args) {
		case 1:
			snap, err := app.client.OperDBSnapshot(app.deviceName, dbName)
			if err != nil {
				return err
			}
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(snap)
			}
			tables := make([]string, 0, len(snap))
			for tbl := range snap {
				tables = append(tables, tbl)
			}
			sort.Strings(tables)
			t := cli.NewTable("TABLE", "ENTRIES")
			for _, tbl := range tables {
				t.Row(tbl, fmt.Sprintf("%d", len(snap[tbl])))
			}
			t.Flush()

		case 2:
			entries, err := app.client.OperDBTable(app.deviceName, dbName, args[1])
			if err != nil {
				return err
			}
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(entries)
			}
			keys := make([]string, 0, len(entries))
			for k := range entries {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if k == "" {
					// Flat-hash table (e.g. COUNTERS_PORT_NAME_MAP): the table
					// IS the entry — print its fields instead of a blank key.
					printFieldTable(entries[k])
					continue
				}
				fmt.Println(k)
			}

		case 3:
			fields, err := app.client.OperDBEntry(app.deviceName, dbName, args[1], args[2])
			if err != nil {
				return err
			}
			if app.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(fields)
			}
			printFieldTable(fields)
		}

		return nil
	},
}

// printFieldTable prints a hash entry's fields sorted by name.
func printFieldTable(fields map[string]string) {
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
}
