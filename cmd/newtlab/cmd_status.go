package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

var (
	jsonOutput      bool
	showBridgeStats bool
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [topology]",
		Short: "Show VM status",
		Long: `Show status of deployed labs.

Without arguments, shows all deployed labs.
With a topology name, shows detailed status for that lab.

  newtlab status                      # all labs
  newtlab status 2node                # detailed view
  newtlab status 2node --bridge-stats # include link connectivity
  newtlab status --json               # machine-readable output`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// No args and no -S: show all deployed labs
			if len(args) == 0 && specDir == "" {
				return showAllLabs()
			}

			// Specific lab
			labName, err := resolveLabName(args)
			if err != nil {
				return err
			}
			return showLabDetail(labName)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	cmd.Flags().BoolVar(&showBridgeStats, "bridge-stats", false, "include link connectivity and traffic counters")
	return cmd
}

func showAllLabs() error {
	labs, err := newtlab.ListLabs()
	if err != nil {
		return err
	}
	if len(labs) == 0 {
		if jsonOutput {
			fmt.Println("[]")
			return nil
		}
		fmt.Println("no deployed labs")
		return nil
	}

	if jsonOutput {
		var states []*newtlab.LabState
		for _, labName := range labs {
			state, err := newtlab.LoadState(labName)
			if err != nil {
				continue
			}
			states = append(states, state)
		}
		return json.NewEncoder(os.Stdout).Encode(states)
	}

	for i, labName := range labs {
		if i > 0 {
			fmt.Println()
		}
		if err := showLabDetail(labName); err != nil {
			fmt.Printf("Lab: %s (error: %v)\n", labName, err)
		}
	}
	return nil
}

func showLabDetail(labName string) error {
	lab := &newtlab.Lab{Name: labName}
	state, err := lab.Status()
	if err != nil {
		return err
	}

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(state)
	}

	fmt.Printf("Lab: %s (deployed %s)\n", state.Name, state.Created.Format("2006-01-02 15:04:05"))
	fmt.Printf("Spec dir: %s\n\n", state.SpecDir)

	// Node table
	t := cli.NewTable("NODE", "STATUS", "SSH PORT", "CONSOLE", "PID")
	for name, node := range state.Nodes {
		var displayStatus string
		switch {
		case node.Status == "error":
			displayStatus = red("error")
		case node.Status == "stopped":
			displayStatus = yellow("stopped")
		case node.Phase != "":
			displayStatus = yellow(node.Phase)
		default:
			displayStatus = green(node.Status)
		}
		t.Row(name, displayStatus, fmt.Sprintf("%d", node.SSHPort), fmt.Sprintf("%d", node.ConsolePort), fmt.Sprintf("%d", node.PID))
	}
	t.Flush()

	// Link table
	if len(state.Links) > 0 {
		fmt.Println()
		if showBridgeStats {
			showLinkTableWithStats(labName, state)
		} else {
			lt := cli.NewTable("LINK", "A_PORT", "Z_PORT")
			for _, link := range state.Links {
				lt.Row(fmt.Sprintf("%s ↔ %s", link.A, link.Z), fmt.Sprintf("%d", link.APort), fmt.Sprintf("%d", link.ZPort))
			}
			lt.Flush()
		}
	}

	return nil
}

// showLinkTableWithStats prints a link table enriched with live bridge stats.
func showLinkTableWithStats(labName string, state *newtlab.LabState) {
	stats, statsErr := newtlab.QueryAllBridgeStats(labName)

	// Build lookup: "A|Z" → LinkStats
	statsMap := map[string]*newtlab.LinkStats{}
	if statsErr == nil {
		for i := range stats.Links {
			ls := &stats.Links[i]
			key := ls.A + "|" + ls.Z
			statsMap[key] = ls
		}
	}

	lt := cli.NewTable("LINK", "STATUS", "A→Z", "Z→A", "SESSIONS")
	for _, link := range state.Links {
		label := fmt.Sprintf("%s ↔ %s", link.A, link.Z)
		key := link.A + "|" + link.Z

		if ls, ok := statsMap[key]; ok {
			if ls.Connected {
				lt.Row(label, green("connected"), humanBytes(ls.AToZBytes), humanBytes(ls.ZToABytes), fmt.Sprintf("%d", ls.Sessions))
			} else {
				lt.Row(label, yellow("waiting"), "—", "—", "—")
			}
		} else {
			lt.Row(label, "—", "—", "—", "—")
		}
	}
	lt.Flush()
}
