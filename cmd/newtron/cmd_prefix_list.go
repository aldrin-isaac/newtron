package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtron"
)

var prefixListCmd = &cobra.Command{
	Use:   "prefix-list",
	Short: "Manage prefix list templates",
	Long: `Manage prefix list templates in network.json (spec authoring).

Prefix lists define reusable sets of IP prefixes that can be referenced
by filters (via src-prefix-list/dst-prefix-list) and route policies.

All commands operate on network.json (no device needed).

Examples:
  newtron prefix-list list
  newtron prefix-list show rfc1918
  newtron prefix-list create rfc1918 --prefixes 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
  newtron prefix-list add-entry rfc1918 198.51.100.0/24
  newtron prefix-list remove-entry rfc1918 198.51.100.0/24
  newtron prefix-list delete rfc1918`,
}

var prefixListListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all prefix list templates",
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := app.client.ListPrefixLists()
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(names)
		}

		if len(names) == 0 {
			fmt.Println("No prefix list templates defined")
			return nil
		}

		sort.Strings(names)

		t := cli.NewTable("NAME", "PREFIXES")

		for _, name := range names {
			detail, err := app.client.ShowPrefixList(name)
			if err != nil {
				continue
			}
			t.Row(name, fmt.Sprintf("%d", len(detail.Prefixes)))
		}
		t.Flush()

		return nil
	},
}

var prefixListShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show prefix list template details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		detail, err := app.client.ShowPrefixList(name)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(detail)
		}

		fmt.Printf("Prefix List: %s\n", bold(detail.Name))
		fmt.Printf("Prefixes: %d\n", len(detail.Prefixes))
		fmt.Println()

		if len(detail.Prefixes) == 0 {
			fmt.Println("  (no prefixes configured)")
			return nil
		}

		for _, p := range detail.Prefixes {
			fmt.Printf("  %s\n", p)
		}

		return nil
	},
}

var prefixListCreatePrefixes string

var prefixListCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new prefix list template",
	Long: `Create a new prefix list template in network.json.

Optionally supply an initial set of prefixes via --prefixes (comma-separated).
Use 'prefix-list add-entry' to add prefixes incrementally.

Examples:
  newtron prefix-list create rfc1918 --prefixes 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
  newtron prefix-list create customer-subnets`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		var prefixes []string
		if prefixListCreatePrefixes != "" {
			for _, p := range strings.Split(prefixListCreatePrefixes, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					prefixes = append(prefixes, p)
				}
			}
		}

		fmt.Printf("Prefix list: %s", name)
		if len(prefixes) > 0 {
			fmt.Printf(" (%d prefixes)", len(prefixes))
		}
		fmt.Println()

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.client.CreatePrefixList(newtron.CreatePrefixListRequest{
			Name:     name,
			Prefixes: prefixes,
		}, execOpts())
	},
}

var prefixListDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a prefix list template",
	Long: `Delete a prefix list template from network.json.

Fails if any filter or route policy references the prefix list.

Examples:
  newtron prefix-list delete rfc1918`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		fmt.Printf("Deleting prefix list: %s\n", name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.client.DeletePrefixList(name, execOpts())
	},
}

var prefixListAddEntryCmd = &cobra.Command{
	Use:   "add-entry <name> <prefix>",
	Short: "Add a prefix to a prefix list template",
	Long: `Add a prefix to an existing prefix list template in network.json.

Examples:
  newtron prefix-list add-entry rfc1918 198.51.100.0/24
  newtron prefix-list add-entry customer-subnets 203.0.113.0/24`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		prefix := args[1]

		fmt.Printf("Adding prefix %s to prefix list '%s'\n", green(prefix), name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.client.AddPrefixListEntry(newtron.AddPrefixListEntryRequest{
			PrefixList: name,
			Prefix:     prefix,
		}, execOpts())
	},
}

var prefixListRemoveEntryCmd = &cobra.Command{
	Use:   "remove-entry <name> <prefix>",
	Short: "Remove a prefix from a prefix list template",
	Long: `Remove a prefix from an existing prefix list template in network.json.

Examples:
  newtron prefix-list remove-entry rfc1918 198.51.100.0/24`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		prefix := args[1]

		fmt.Printf("Removing prefix %s from prefix list '%s'\n", prefix, name)

		if !app.executeMode {
			printDryRunNotice()
			return nil
		}

		return app.client.RemovePrefixListEntry(name, prefix, execOpts())
	},
}

func init() {
	prefixListCreateCmd.Flags().StringVar(&prefixListCreatePrefixes, "prefixes", "", "Comma-separated initial prefixes (e.g. 10.0.0.0/8,172.16.0.0/12)")

	prefixListCmd.AddCommand(prefixListListCmd)
	prefixListCmd.AddCommand(prefixListShowCmd)
	prefixListCmd.AddCommand(prefixListCreateCmd)
	prefixListCmd.AddCommand(prefixListDeleteCmd)
	prefixListCmd.AddCommand(prefixListAddEntryCmd)
	prefixListCmd.AddCommand(prefixListRemoveEntryCmd)
}
