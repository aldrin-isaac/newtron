package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/spf13/cobra"
)

func newConsoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "console <node>",
		Short: "Attach to serial console",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeName := args[0]

			state, _, err := findNodeState(nodeName)
			if err != nil {
				return err
			}

			node, ok := state.Nodes[nodeName]
			if !ok {
				return fmt.Errorf("node %q not found", nodeName)
			}

			port := strconv.Itoa(node.ConsolePort)

			// Try socat first, then telnet
			if socatBin, err := exec.LookPath("socat"); err == nil {
				return syscallExec(socatBin,
					[]string{"socat", "-,rawer", "TCP:127.0.0.1:" + port},
					os.Environ(),
				)
			}

			if telnetBin, err := exec.LookPath("telnet"); err == nil {
				return syscallExec(telnetBin,
					[]string{"telnet", "127.0.0.1", port},
					os.Environ(),
				)
			}

			return fmt.Errorf("neither socat nor telnet found in PATH")
		},
	}
	return cmd
}
