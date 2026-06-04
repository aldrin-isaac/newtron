package newtlab

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Port allocation scheme:
//
//   LinkPortBase (default 20000):
//     Link A/Z ports:    LinkPortBase + i*2, LinkPortBase + i*2 + 1
//     Bridge stats:      LinkPortBase - 1, LinkPortBase - 2, ... (one per worker host)
//   ConsolePortBase (default 30000):
//     Serial console:    ConsolePortBase + nodeIndex
//   SSHPortBase (default 40000):
//     SSH forwarding:    SSHPortBase + nodeIndex
//
// Ranges are non-overlapping: links grow upward from 20000, bridge stats
// grow downward from 19999, consoles start at 30000, SSH starts at 40000.

// PortAllocation describes a single TCP port allocation in the lab.
type PortAllocation struct {
	Host    string // host name ("" = local)
	HostIP  string // resolved IP ("" = 127.0.0.1 for local)
	Port    int
	Purpose string // e.g. "spine1 SSH", "link spine1:Ethernet0 A-side"
}

// CollectAllPorts gathers all TCP port allocations for the lab:
// SSH ports, console ports, link A/Z ports, and bridge stats ports.
func CollectAllPorts(lab *Lab) []PortAllocation {
	var allocs []PortAllocation

	// Node SSH and console ports
	for _, name := range sortedNodeNames(lab.Nodes) {
		node := lab.Nodes[name]
		hostIP := resolveHostIP(node.Host, lab.Config)
		allocs = append(allocs, PortAllocation{
			Host:    node.Host,
			HostIP:  hostIP,
			Port:    node.SSHPort,
			Purpose: name + " SSH",
		})
		allocs = append(allocs, PortAllocation{
			Host:    node.Host,
			HostIP:  hostIP,
			Port:    node.ConsolePort,
			Purpose: name + " console",
		})
	}

	// Link A/Z ports
	for _, lc := range lab.Links {
		workerIP := resolveHostIP(lc.WorkerHost, lab.Config)
		allocs = append(allocs, PortAllocation{
			Host:    lc.WorkerHost,
			HostIP:  workerIP,
			Port:    lc.APort,
			Purpose: fmt.Sprintf("link %s:%s A-side", lc.A.Device, lc.A.Interface),
		})
		allocs = append(allocs, PortAllocation{
			Host:    lc.WorkerHost,
			HostIP:  workerIP,
			Port:    lc.ZPort,
			Purpose: fmt.Sprintf("link %s:%s Z-side", lc.Z.Device, lc.Z.Interface),
		})
	}

	// Bridge stats ports (one per unique worker host)
	statsPorts := allocateBridgeStatsPorts(lab)
	for host, port := range statsPorts {
		hostIP := resolveHostIP(host, lab.Config)
		allocs = append(allocs, PortAllocation{
			Host:    host,
			HostIP:  hostIP,
			Port:    port,
			Purpose: fmt.Sprintf("bridge stats (%s)", hostDisplayName(host)),
		})
	}

	return allocs
}

// allocateBridgeStatsPorts returns the stats port for each bridge worker host.
// Mirrors the allocation logic in Deploy(): counting down from LinkPortBase - 1.
func allocateBridgeStatsPorts(lab *Lab) map[string]int {
	if len(lab.Links) == 0 {
		return nil
	}

	// Collect unique worker hosts in sorted order
	hostSet := map[string]bool{}
	for _, lc := range lab.Links {
		hostSet[lc.WorkerHost] = true
	}
	hosts := make([]string, 0, len(hostSet))
	for h := range hostSet {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	result := make(map[string]int, len(hosts))
	nextPort := lab.Config.LinkPortBase - 1
	for _, host := range hosts {
		result[host] = nextPort
		nextPort--
	}
	return result
}

// hostDisplayName returns a display name for a host ("local" for empty string).
func hostDisplayName(host string) string {
	if host == "" {
		return "local"
	}
	return host
}

// portOwner describes which lab (and which process within that lab) holds a
// TCP port. Populated by attributePortOwners from each lab's state.json so
// ProbeAllPorts can name the conflicting lab in its error rather than just
// reporting a bare "port N in use" (issue #67).
type portOwner struct {
	Lab     string // lab name (matches the directory under ~/.newtlab/labs/)
	PID     int    // owning process PID; 0 when the state record doesn't store one
	Purpose string // "SSH", "console", "link bridge", "bridge stats"
}

// attributePortOwners walks every lab's state.json and indexes the ports each
// lab holds. excludeLab is skipped so the lab being deployed doesn't flag
// itself during a redeploy.
//
// Errors are absorbed silently — a corrupt or partially-written state.json on
// some unrelated lab must not block probing the lab currently being deployed.
// In the worst case attribution is best-effort; the bare error format is the
// fallback.
func attributePortOwners(excludeLab string) map[int]portOwner {
	names, err := ListLabs()
	if err != nil {
		return nil
	}
	owners := map[int]portOwner{}
	for _, name := range names {
		if name == excludeLab {
			continue
		}
		state, err := LoadState(name)
		if err != nil {
			continue
		}
		for _, node := range state.Nodes {
			if node.SSHPort > 0 {
				owners[node.SSHPort] = portOwner{Lab: state.Name, PID: node.PID, Purpose: "SSH"}
			}
			if node.ConsolePort > 0 {
				owners[node.ConsolePort] = portOwner{Lab: state.Name, PID: node.PID, Purpose: "console"}
			}
		}
		bridgePIDByHost := map[string]int{}
		for host, br := range state.Bridges {
			bridgePIDByHost[host] = br.PID
			if _, portStr, splitErr := net.SplitHostPort(br.StatsAddr); splitErr == nil {
				if port, atoiErr := strconv.Atoi(portStr); atoiErr == nil && port > 0 {
					owners[port] = portOwner{Lab: state.Name, PID: br.PID, Purpose: "bridge stats"}
				}
			}
		}
		for _, link := range state.Links {
			pid := bridgePIDByHost[link.WorkerHost]
			if link.APort > 0 {
				owners[link.APort] = portOwner{Lab: state.Name, PID: pid, Purpose: "link bridge"}
			}
			if link.ZPort > 0 {
				owners[link.ZPort] = portOwner{Lab: state.Name, PID: pid, Purpose: "link bridge"}
			}
		}
	}
	return owners
}

// formatPortConflict produces the user-facing error line for one conflicting
// port. When the port is held by another newtlab-managed lab, the message
// names that lab and suggests the remediation command. Otherwise it falls
// back to the bare "port N in use" form so the operator still sees the
// conflict (just without the lab attribution).
func formatPortConflict(purpose string, port int, hostIP string, owners map[int]portOwner) string {
	suffix := ""
	if hostIP != "" {
		suffix = " on " + hostIP
	}
	if owner, ok := owners[port]; ok {
		ownership := fmt.Sprintf("lab %q (%s", owner.Lab, owner.Purpose)
		if owner.PID > 0 {
			ownership += fmt.Sprintf(", PID %d", owner.PID)
		}
		ownership += ")"
		return fmt.Sprintf("  %s: port %d%s held by %s; run 'newtlab destroy %s' first",
			purpose, port, suffix, ownership, owner.Lab)
	}
	return fmt.Sprintf("  %s: port %d%s in use", purpose, port, suffix)
}

// ProbeAllPorts checks that all allocated ports are free. Local ports are
// tested with net.Listen; remote ports via SSH + ss. excludeLab is the name
// of the lab being deployed — its own ports are not attributed to itself
// when a redeploy collides with a stale entry. Pass "" if no exclusion.
//
// Returns a multi-error listing every conflict, with each line naming the
// owning lab (and PID, when known) so the operator can stop or destroy it.
func ProbeAllPorts(allocations []PortAllocation, excludeLab string) error {
	owners := attributePortOwners(excludeLab)

	byHost := map[string][]PortAllocation{}
	for _, a := range allocations {
		key := a.HostIP
		if a.Host == "" {
			key = "" // local
		}
		byHost[key] = append(byHost[key], a)
	}

	var errs []string

	if locals, ok := byHost[""]; ok {
		for _, a := range locals {
			if err := probePortLocal(a.Port); err != nil {
				errs = append(errs, formatPortConflict(a.Purpose, a.Port, "", owners))
			}
		}
	}

	for hostIP, allocs := range byHost {
		if hostIP == "" {
			continue
		}
		ports := make([]int, len(allocs))
		for i, a := range allocs {
			ports[i] = a.Port
		}
		conflicts := probePortsRemote(hostIP, ports)
		for port := range conflicts {
			for _, a := range allocs {
				if a.Port == port {
					errs = append(errs, formatPortConflict(a.Purpose, port, hostIP, owners))
					break
				}
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

// probePortLocal attempts net.Listen on the given port to check availability.
// Returns error if the port is in use.
func probePortLocal(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("port %d already in use", port)
	}
	ln.Close()
	return nil
}

// findFreeLocalPort finds a free local TCP port starting from preferred,
// skipping any ports in the avoid set. Searches up to 100 ports above preferred.
func findFreeLocalPort(preferred int, avoid map[int]bool) (int, error) {
	for port := preferred; port < preferred+100; port++ {
		if avoid[port] {
			continue
		}
		if err := probePortLocal(port); err == nil {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found in range %d-%d", preferred, preferred+99)
}

// probePortsRemote checks port availability on a remote host via SSH + ss.
// Returns a map of port → error for ports that are in use.
func probePortsRemote(hostIP string, ports []int) map[int]error {
	if len(ports) == 0 {
		return nil
	}

	// Build ss filter: "sport = :PORT1 or sport = :PORT2 ..."
	filters := make([]string, len(ports))
	for i, p := range ports {
		filters[i] = fmt.Sprintf("sport = :%d", p)
	}
	ssFilter := strings.Join(filters, " or ")
	ssCmd := fmt.Sprintf("ss -tlnH '( %s )'", ssFilter)

	cmd := sshCommand(hostIP, ssCmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// SSH failure — can't verify, return all ports as potentially conflicting
		result := make(map[int]error, len(ports))
		for _, p := range ports {
			result[p] = fmt.Errorf("SSH probe failed: %v", err)
		}
		return result
	}

	// Parse ss output to find which ports are in use.
	// Each output line contains the local address as "addr:port" or "*:port".
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil // no ports in use
	}

	inUse := map[int]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		// ss -tln output: State Recv-Q Send-Q Local Address:Port Peer Address:Port
		// With -H (no header), fields[3] is the local address
		if len(fields) >= 4 {
			localAddr := fields[3]
			if idx := strings.LastIndex(localAddr, ":"); idx >= 0 {
				portStr := localAddr[idx+1:]
				for _, p := range ports {
					if portStr == fmt.Sprintf("%d", p) {
						inUse[p] = true
					}
				}
			}
		}
	}

	if len(inUse) == 0 {
		return nil
	}

	result := make(map[int]error, len(inUse))
	for p := range inUse {
		result[p] = fmt.Errorf("port in use")
	}
	return result
}
