package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show VM status",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine lab name
			var labName string
			if specDir != "" {
				lab, err := newtlab.NewLab(specDir)
				if err != nil {
					return err
				}
				labName = lab.Name
			} else {
				labs, err := newtlab.ListLabs()
				if err != nil {
					return err
				}
				if len(labs) == 0 {
					return fmt.Errorf("no labs found")
				}
				if len(labs) > 1 {
					return fmt.Errorf("multiple labs found, specify with -S: %v", labs)
				}
				labName = labs[0]
			}

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
				fmt.Printf("\n%-40s %-8s\n", "LINK", "PORT")
				fmt.Printf("%-40s %-8s\n", "────────────────────────────────────────", "────────")
				for _, link := range state.Links {
					fmt.Printf("%-40s %-8d\n",
						fmt.Sprintf("%s ↔ %s", link.A, link.Z),
						link.Port,
					)
				}
			}

			return nil
		},
	}
	return cmd
}
