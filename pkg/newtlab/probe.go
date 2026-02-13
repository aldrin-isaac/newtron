package newtlab

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strings"
)

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
		hostIP := resolveHostIP(node, lab.Config)
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
		workerIP := resolveWorkerHostIP(lc.WorkerHost, lab.Config)
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
		hostIP := resolveWorkerHostIP(host, lab.Config)
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

// ProbeAllPorts checks that all allocated ports are free.
// Local ports are tested with net.Listen. Remote ports are tested via SSH + ss.
// Returns a multi-error listing all conflicts.
func ProbeAllPorts(allocations []PortAllocation) error {
	// Group by resolved host IP (empty string = local)
	byHost := map[string][]PortAllocation{}
	for _, a := range allocations {
		key := a.HostIP
		if a.Host == "" {
			key = "" // local
		}
		byHost[key] = append(byHost[key], a)
	}

	var errs []string

	// Probe local ports
	if locals, ok := byHost[""]; ok {
		for _, a := range locals {
			if err := probePortLocal(a.Port); err != nil {
				errs = append(errs, fmt.Sprintf("  %s: port %d in use", a.Purpose, a.Port))
			}
		}
	}

	// Probe remote ports (one SSH per host)
	for hostIP, allocs := range byHost {
		if hostIP == "" {
			continue
		}
		ports := make([]int, len(allocs))
		for i, a := range allocs {
			ports[i] = a.Port
		}
		conflicts := probePortsRemote(hostIP, ports)
		for port, err := range conflicts {
			// Find the allocation for this port to get its purpose
			for _, a := range allocs {
				if a.Port == port {
					errs = append(errs, fmt.Sprintf("  %s: port %d in use on %s (%v)", a.Purpose, port, hostIP, err))
					break
				}
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
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
