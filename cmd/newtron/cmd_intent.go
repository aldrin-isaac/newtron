package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
)

var intentCmd = &cobra.Command{
	Use:   "intent",
	Short: "Intent DAG operations",
	Long:  `View and inspect the intent DAG on a device.`,
}

var intentAncestors bool

var intentTreeCmd = &cobra.Command{
	Use:   "tree [resource-kind[:<resource>]]",
	Short: "Display the intent DAG as a tree",
	Long: `Display the intent DAG as a tree, rooted at the device or scoped to a
specific resource kind or resource.

Forms:
  newtron -D switch1 intent tree                    # full tree from device root
  newtron -D switch1 intent tree vlan               # all VLAN subtrees
  newtron -D switch1 intent tree vlan:100            # specific VLAN subtree
  newtron -D switch1 intent tree interface:Ethernet0 # specific interface subtree
  newtron -D switch1 intent tree vlan:100 --ancestors # include path to root`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		var kind, resource string
		if len(args) == 1 {
			parts := strings.SplitN(args[0], ":", 2)
			kind = parts[0]
			if len(parts) == 2 {
				resource = parts[1]
			}
		}

		nodes, err := app.client.IntentTree(app.deviceName, kind, resource, intentAncestors)
		if err != nil {
			return err
		}

		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(nodes)
		}

		if len(nodes) == 0 {
			fmt.Println("No intent records found.")
			return nil
		}

		for i, node := range nodes {
			if i > 0 {
				fmt.Println()
			}
			printIntentTree(node, "", true)
		}
		return nil
	},
}

// printIntentTree recursively renders an IntentTreeNode as a tree.
func printIntentTree(node newtron.IntentTreeNode, prefix string, isRoot bool) {
	// Print this node
	var line string
	if isRoot {
		line = fmt.Sprintf("%s (%s)", node.Resource, node.Operation)
	} else {
		line = fmt.Sprintf("%s (%s)", node.Resource, node.Operation)
	}
	if params := formatIntentParams(node.Params); params != "" {
		line += " " + params
	}
	fmt.Println(line)

	if node.Leaf {
		return
	}

	// Print children
	for i, child := range node.Children {
		isLast := i == len(node.Children)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		childPrefix := prefix + "│   "
		if isLast {
			childPrefix = prefix + "    "
		}

		fmt.Print(prefix + connector)
		printIntentTree(child, childPrefix, false)
	}
}

// formatIntentParams formats params as key=value pairs for display.
func formatIntentParams(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, " ")
}

func init() {
	intentTreeCmd.Flags().BoolVar(&intentAncestors, "ancestors", false, "Show path from resource to root")
	intentCmd.AddCommand(intentTreeCmd)
}
