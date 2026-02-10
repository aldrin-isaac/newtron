package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newBridgeStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge-stats",
		Short: "Show live bridge telemetry",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			stats, err := newtlab.QueryAllBridgeStats(labName)
			if err != nil {
				return err
			}

			fmt.Printf("%-40s %-12s %-12s %-9s %s\n",
				"LINK", "A\u2192Z", "Z\u2192A", "SESSIONS", "CONNECTED")
			fmt.Printf("%-40s %-12s %-12s %-9s %s\n",
				"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500",
				"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500",
				"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500",
				"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500",
				"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

			for _, ls := range stats.Links {
				label := fmt.Sprintf("%s \u2194 %s", ls.A, ls.Z)
				conn := "no"
				if ls.Connected {
					conn = green("yes")
				}
				fmt.Printf("%-40s %-12s %-12s %-9d %s\n",
					label,
					humanBytes(ls.AToZBytes),
					humanBytes(ls.ZToABytes),
					ls.Sessions,
					conn,
				)
			}

			return nil
		},
	}
	return cmd
}

// humanBytes formats a byte count into a human-readable string.
func humanBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
