package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/cli"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

var jsonOutput bool

func newStatusCmd() *cobra.Command {
	var monitor bool

	cmd := &cobra.Command{
		Use:   "status [topology]",
		Short: "Show VM status",
		Long: `Show status of deployed labs.

Without arguments, shows all deployed labs.
With a topology name, shows detailed status for that lab.

  newtlab status                      # all labs
  newtlab status 2node                # detailed view
  newtlab status 2node --monitor      # auto-refresh every 2s
  newtlab status --json               # machine-readable output`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// No args and no -S: show all deployed labs
			if len(args) == 0 && specDir == "" {
				if monitor {
					return monitorAllLabs()
				}
				return showAllLabs()
			}

			// Specific lab
			labName, err := resolveLabName(args)
			if err != nil {
				return err
			}
			if monitor {
				return monitorLab(labName, nil)
			}
			return showLabDetail(labName)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "JSON output")
	cmd.Flags().BoolVarP(&monitor, "monitor", "m", false, "auto-refresh every 2s (Ctrl+C to stop)")
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

	// Detect if any node is on a remote host
	hasRemoteHost := false
	for _, node := range state.Nodes {
		if node.HostIP != "" {
			hasRemoteHost = true
			break
		}
	}

	// Sort node names for stable output
	nodeNames := make([]string, 0, len(state.Nodes))
	for name := range state.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	// Node table with conditional HOST column
	var t *cli.Table
	if hasRemoteHost {
		t = cli.NewTable("NODE", "TYPE", "STATUS", "HOST", "IMAGE", "SSH", "CONSOLE", "PID")
	} else {
		t = cli.NewTable("NODE", "TYPE", "STATUS", "IMAGE", "SSH", "CONSOLE", "PID")
	}
	for _, name := range nodeNames {
		node := state.Nodes[name]
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

		nodeType := "switch"
		switch {
		case node.DeviceType == "host-vm":
			nodeType = "host-vm"
		case node.VMName != "":
			nodeType = fmt.Sprintf("vhost:%s/%s", node.VMName, node.Namespace)
		}

		// Display basename of image path, strip common extensions for readability
		imageDisplay := filepath.Base(node.Image)
		if imageDisplay == "" || imageDisplay == "." {
			imageDisplay = "—"
		} else {
			// Strip .qcow2, .img, .raw extensions
			imageDisplay = strings.TrimSuffix(imageDisplay, ".qcow2")
			imageDisplay = strings.TrimSuffix(imageDisplay, ".img")
			imageDisplay = strings.TrimSuffix(imageDisplay, ".raw")
		}

		if hasRemoteHost {
			hostDisplay := "local"
			if node.HostIP != "" {
				hostDisplay = node.HostIP
			}
			t.Row(name, nodeType, displayStatus, hostDisplay, imageDisplay,
				fmt.Sprintf("%d", node.SSHPort), fmt.Sprintf("%d", node.ConsolePort), fmt.Sprintf("%d", node.PID))
		} else {
			t.Row(name, nodeType, displayStatus, imageDisplay,
				fmt.Sprintf("%d", node.SSHPort), fmt.Sprintf("%d", node.ConsolePort), fmt.Sprintf("%d", node.PID))
		}
	}
	t.Flush()

	// Link table (sort for stable output)
	if len(state.Links) > 0 {
		sort.Slice(state.Links, func(i, j int) bool {
			if state.Links[i].A != state.Links[j].A {
				return state.Links[i].A < state.Links[j].A
			}
			return state.Links[i].Z < state.Links[j].Z
		})
		fmt.Println()
		showLinkTableWithStats(labName, state)
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

	// Include HOST column if any link has a non-local worker host.
	hasRemoteHost := false
	for _, link := range state.Links {
		if link.WorkerHost != "" {
			hasRemoteHost = true
			break
		}
	}

	var lt *cli.Table
	if hasRemoteHost {
		lt = cli.NewTable("LINK", "STATUS", "HOST", "A→Z", "Z→A", "SESSIONS")
	} else {
		lt = cli.NewTable("LINK", "STATUS", "A→Z", "Z→A", "SESSIONS")
	}
	for _, link := range state.Links {
		label := fmt.Sprintf("%s ↔ %s", link.A, link.Z)
		key := link.A + "|" + link.Z
		host := link.WorkerHost
		if host == "" {
			host = "local"
		}

		if ls, ok := statsMap[key]; ok {
			if ls.Connected {
				if hasRemoteHost {
					lt.Row(label, green("connected"), host, humanBytes(ls.AToZBytes), humanBytes(ls.ZToABytes), fmt.Sprintf("%d", ls.Sessions))
				} else {
					lt.Row(label, green("connected"), humanBytes(ls.AToZBytes), humanBytes(ls.ZToABytes), fmt.Sprintf("%d", ls.Sessions))
				}
			} else {
				if hasRemoteHost {
					lt.Row(label, yellow("waiting"), host, "—", "—", "—")
				} else {
					lt.Row(label, yellow("waiting"), "—", "—", "—")
				}
			}
		} else {
			if hasRemoteHost {
				lt.Row(label, "—", host, "—", "—", "—")
			} else {
				lt.Row(label, "—", "—", "—", "—")
			}
		}
	}
	lt.Flush()
}

// monitorLab auto-refreshes a single lab's status every 2 seconds.
// Exits when deployment is complete (all nodes running with no phase) or on Ctrl+C.
func monitorLab(labName string, done <-chan struct{}) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	for {
		fmt.Print("\033[2J\033[H") // clear screen, cursor to top
		if err := showLabDetail(labName); err != nil {
			fmt.Printf("  Waiting for %s to start...\n", labName)
		}

		if labDeployFinished(labName) {
			return nil
		}

		select {
		case <-sigCh:
			return nil
		case <-done:
			// Deploy goroutine finished (success or error) — stop monitoring.
			// Show final state before returning.
			fmt.Print("\033[2J\033[H")
			_ = showLabDetail(labName)
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// monitorAllLabs auto-refreshes all labs' status every 2 seconds.
// Exits when all labs finish deploying or on Ctrl+C.
func monitorAllLabs() error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	for {
		fmt.Print("\033[2J\033[H") // clear screen, cursor to top
		if err := showAllLabs(); err != nil {
			fmt.Printf("  error: %v\n", err)
		}

		// Check all deployed labs
		labs, _ := newtlab.ListLabs()
		allDone := len(labs) > 0
		for _, name := range labs {
			if !labDeployFinished(name) {
				allDone = false
				break
			}
		}
		if allDone {
			return nil
		}

		select {
		case <-sigCh:
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// labDeployFinished returns true when a lab is no longer deploying:
// all nodes are running with no phase, or all have reached a terminal state
// (stopped/error).
func labDeployFinished(labName string) bool {
	lab := &newtlab.Lab{Name: labName}
	state, err := lab.Status()
	if err != nil || len(state.Nodes) == 0 {
		return false
	}
	for _, node := range state.Nodes {
		if node.Phase != "" {
			return false // still in a deploy phase
		}
	}
	return true
}
