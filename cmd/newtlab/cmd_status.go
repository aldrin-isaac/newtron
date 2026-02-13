package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [topology]",
		Short: "Show VM status",
		Long: `Show status of deployed labs.

Without arguments, shows all deployed labs.
With a topology name, shows detailed status for that lab.

  newtlab status           # all labs
  newtlab status 2node     # detailed view`,
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
	return cmd
}

func showAllLabs() error {
	labs, err := newtlab.ListLabs()
	if err != nil {
		return err
	}
	if len(labs) == 0 {
		fmt.Println("no deployed labs")
		return nil
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

	fmt.Printf("Lab: %s (deployed %s)\n", state.Name, state.Created.Format("2006-01-02 15:04:05"))
	fmt.Printf("Spec dir: %s\n\n", state.SpecDir)

	// Node table
	fmt.Printf("%-16s %-10s %-12s %-12s %s\n", "NODE", "STATUS", "SSH PORT", "CONSOLE", "PID")
	fmt.Printf("%-16s %-10s %-12s %-12s %s\n", "────────────────", "──────────", "────────────", "────────────", "──────")
	for name, node := range state.Nodes {
		status := node.Status
		switch status {
		case "running":
			status = green(status)
		case "error":
			status = red(status)
		case "stopped":
			status = yellow(status)
		}
		fmt.Printf("%-16s %-10s %-12d %-12d %d\n", name, status, node.SSHPort, node.ConsolePort, node.PID)
	}

	// Link table
	if len(state.Links) > 0 {
		fmt.Printf("\n%-40s %-8s %-8s\n", "LINK", "A_PORT", "Z_PORT")
		fmt.Printf("%-40s %-8s %-8s\n", "────────────────────────────────────────", "────────", "────────")
		for _, link := range state.Links {
			fmt.Printf("%-40s %-8d %-8d\n",
				fmt.Sprintf("%s ↔ %s", link.A, link.Z),
				link.APort, link.ZPort,
			)
		}
	}

	return nil
}
