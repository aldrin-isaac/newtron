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
		Long: `Attach to a VM's serial console via socat or telnet.

Useful for debugging boot issues when SSH is not yet available.
Press Ctrl+] to detach from telnet, or Ctrl+C for socat.

  newtlab console leaf1`,
		Args: cobra.ExactArgs(1),
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

			host := "127.0.0.1"
			if node.HostIP != "" {
				host = node.HostIP
			}
			port := strconv.Itoa(node.ConsolePort)

			// Try socat first, then telnet
			if socatBin, err := exec.LookPath("socat"); err == nil {
				return syscallExec(socatBin,
					[]string{"socat", "-,rawer", "TCP:" + host + ":" + port},
					os.Environ(),
				)
			}

			if telnetBin, err := exec.LookPath("telnet"); err == nil {
				return syscallExec(telnetBin,
					[]string{"telnet", host, port},
					os.Environ(),
				)
			}

			return fmt.Errorf("neither socat nor telnet found in PATH")
		},
	}
	return cmd
}
