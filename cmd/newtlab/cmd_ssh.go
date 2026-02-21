package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtlab"
)

func newSSHCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh <node>",
		Short: "SSH to a VM",
		Long: `Open an SSH session to a deployed VM by node name.

The node is found by searching all deployed labs. Use -S to limit
the search to a specific lab.

  newtlab ssh leaf1
  newtlab ssh spine1`,
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

			// Exec into ssh
			sshBin, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH")
			}

			host := "127.0.0.1"
			if node.HostIP != "" {
				host = node.HostIP
			}

			// Base SSH args — always disable host key checking for lab VMs.
			// Use the lab key when available for passwordless access.
			baseArgs := []string{"ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
			}
			if state.SSHKeyPath != "" {
				baseArgs = append(baseArgs, "-i", state.SSHKeyPath, "-o", "PasswordAuthentication=no")
			}

			// Virtual host: SSH to parent VM and exec into namespace
			if node.Namespace != "" {
				user := node.SSHUser
				if user == "" {
					user = "root"
				}
				sshArgs := append(baseArgs,
					"-t",
					"-p", strconv.Itoa(node.SSHPort),
					user+"@"+host,
					fmt.Sprintf("ip netns exec %s bash", node.Namespace),
				)
				return syscallExec(sshBin, sshArgs, os.Environ())
			}

			// Regular node or host-vm
			user := "admin"
			if node.SSHUser != "" {
				user = node.SSHUser
			}

			sshArgs := append(baseArgs,
				"-p", strconv.Itoa(node.SSHPort),
				user+"@"+host,
			)
			return syscallExec(sshBin, sshArgs, os.Environ())
		},
	}
	return cmd
}

// findNodeState searches all labs for a node by name.
func findNodeState(nodeName string) (*newtlab.LabState, string, error) {
	if specDir != "" {
		lab, err := newtlab.NewLab(specDir)
		if err != nil {
			return nil, "", err
		}
		state, err := lab.Status()
		if err != nil {
			return nil, "", err
		}
		return state, lab.Name, nil
	}

	labs, err := newtlab.ListLabs()
	if err != nil {
		return nil, "", err
	}

	for _, labName := range labs {
		state, err := newtlab.LoadState(labName)
		if err != nil {
			continue
		}
		if _, ok := state.Nodes[nodeName]; ok {
			return state, labName, nil
		}
	}

	// Collect all known node names for a helpful error message.
	var available []string
	for _, labName := range labs {
		state, err := newtlab.LoadState(labName)
		if err != nil {
			continue
		}
		for name := range state.Nodes {
			available = append(available, name)
		}
	}
	if len(available) > 0 {
		return nil, "", fmt.Errorf("node %q not found; available: %s", nodeName, joinSorted(available))
	}
	return nil, "", fmt.Errorf("node %q not found (no deployed labs)", nodeName)
}

// joinSorted returns names in sorted order, comma-separated.
func joinSorted(names []string) string {
	sort.Strings(names)
	return strings.Join(names, ", ")
}
