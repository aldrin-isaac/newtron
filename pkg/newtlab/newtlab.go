package newtlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/newtron-network/newtron/pkg/spec"
)

// Lab is the top-level newtlab orchestrator. It reads newtron spec files,
// resolves VM configuration, and manages QEMU processes.
type Lab struct {
	Name     string
	SpecDir  string
	StateDir string
	Topology *spec.TopologySpecFile
	Platform *spec.PlatformSpecFile
	Profiles map[string]*spec.DeviceProfile
	Config   *VMLabConfig
	Nodes    map[string]*NodeConfig
	Links    []*LinkConfig
	State    *LabState
	Force    bool
}

// NewLab loads specs from specDir and returns a configured Lab.
func NewLab(specDir string) (*Lab, error) {
	absDir, err := filepath.Abs(specDir)
	if err != nil {
		return nil, fmt.Errorf("newtlab: resolve spec dir: %w", err)
	}

	name := filepath.Base(absDir)
	if name == "specs" {
		name = filepath.Base(filepath.Dir(absDir))
	}

	l := &Lab{
		Name:     name,
		SpecDir:  absDir,
		StateDir: LabDir(name),
		Profiles: make(map[string]*spec.DeviceProfile),
		Nodes:    make(map[string]*NodeConfig),
	}

	// Load topology.json
	topoData, err := os.ReadFile(filepath.Join(absDir, "topology.json"))
	if err != nil {
		return nil, fmt.Errorf("newtlab: read topology.json: %w", err)
	}
	if err := json.Unmarshal(topoData, &l.Topology); err != nil {
		return nil, fmt.Errorf("newtlab: parse topology.json: %w", err)
	}

	// Load platforms.json
	platData, err := os.ReadFile(filepath.Join(absDir, "platforms.json"))
	if err != nil {
		return nil, fmt.Errorf("newtlab: read platforms.json: %w", err)
	}
	if err := json.Unmarshal(platData, &l.Platform); err != nil {
		return nil, fmt.Errorf("newtlab: parse platforms.json: %w", err)
	}

	// Load device profiles
	for deviceName := range l.Topology.Devices {
		profilePath := filepath.Join(absDir, "profiles", deviceName+".json")
		data, err := os.ReadFile(profilePath)
		if err != nil {
			return nil, fmt.Errorf("newtlab: read profile %s: %w", deviceName, err)
		}
		var profile spec.DeviceProfile
		if err := json.Unmarshal(data, &profile); err != nil {
			return nil, fmt.Errorf("newtlab: parse profile %s: %w", deviceName, err)
		}
		l.Profiles[deviceName] = &profile
	}

	// Resolve newtlab config with defaults
	l.Config = resolveNewtLabConfig(l.Topology.NewtLab)

	// Resolve node configs
	sortedNames := l.Topology.DeviceNames()
	for i, name := range sortedNames {
		profile := l.Profiles[name]
		var platform *spec.PlatformSpec
		if profile.Platform != "" {
			platform = l.Platform.Platforms[profile.Platform]
		}

		nc, err := ResolveNodeConfig(name, profile, platform)
		if err != nil {
			return nil, err
		}

		// Allocate SSH and console ports
		nc.SSHPort = l.Config.SSHPortBase + i
		nc.ConsolePort = l.Config.ConsolePortBase + i

		l.Nodes[name] = nc
	}

	// Auto-place nodes across server pool (if configured)
	if len(l.Config.Servers) > 0 {
		if err := PlaceNodes(l.Nodes, l.Config.Servers); err != nil {
			return nil, err
		}
	}

	// Allocate links
	l.Links, err = AllocateLinks(l.Topology.Links, l.Nodes, l.Config)
	if err != nil {
		return nil, err
	}

	return l, nil
}

// Deploy creates overlay disks, starts QEMU processes, waits for SSH,
// and patches profiles.
func (l *Lab) Deploy() error {
	// Check for stale state
	if existing, err := LoadState(l.Name); err == nil && existing != nil {
		if !l.Force {
			return fmt.Errorf("newtlab: lab %s already deployed (created %s); use --force to redeploy",
				l.Name, existing.Created.Format(time.RFC3339))
		}
		l.destroyExisting(existing)
	}

	// Port conflict detection (SSH, console, link, bridge stats — local and remote)
	allocs := CollectAllPorts(l)
	if err := ProbeAllPorts(allocs); err != nil {
		return fmt.Errorf("newtlab: port conflicts:\n%w", err)
	}

	// Create state directories
	for _, sub := range []string{"qemu", "disks", "logs"} {
		if err := os.MkdirAll(filepath.Join(l.StateDir, sub), 0755); err != nil {
			return fmt.Errorf("newtlab: create state dir: %w", err)
		}
	}

	// Initialize state
	l.State = &LabState{
		Name:    l.Name,
		Created: time.Now(),
		SpecDir: l.SpecDir,
		Nodes:   make(map[string]*NodeState),
	}
	for _, lc := range l.Links {
		l.State.Links = append(l.State.Links, &LinkState{
			A:          fmt.Sprintf("%s:%s", lc.A.Device, lc.A.Interface),
			Z:          fmt.Sprintf("%s:%s", lc.Z.Device, lc.Z.Interface),
			APort:      lc.APort,
			ZPort:      lc.ZPort,
			WorkerHost: lc.WorkerHost,
		})
	}

	// Set up remote state directories (one SSH per unique remote host)
	remoteHosts := map[string]bool{}
	for _, node := range l.Nodes {
		hostIP := resolveHostIP(node, l.Config)
		if hostIP != "" && !remoteHosts[hostIP] {
			if err := setupRemoteStateDir(l.Name, hostIP); err != nil {
				return err
			}
			remoteHosts[hostIP] = true
		}
	}

	// Create overlay disks (local or remote)
	for name, node := range l.Nodes {
		hostIP := resolveHostIP(node, l.Config)
		if hostIP != "" {
			// Remote: use ~/-based paths for shell expansion on remote host
			remoteOverlay := fmt.Sprintf("~/.newtlab/labs/%s/disks/%s.qcow2", l.Name, name)
			remoteImage := unexpandHome(node.Image)
			if err := CreateOverlayRemote(remoteImage, remoteOverlay, hostIP); err != nil {
				return err
			}
		} else {
			// Local: expand ~ and use absolute paths
			overlayPath := filepath.Join(l.StateDir, "disks", name+".qcow2")
			if err := CreateOverlay(expandHome(node.Image), overlayPath); err != nil {
				return err
			}
		}
	}

	// Start bridge processes before QEMU so VMs can connect immediately.
	// Group links by WorkerHost and start a separate bridge per host.
	var deployErr error
	if len(l.Links) > 0 {
		hostLinks := map[string][]*LinkConfig{}
		for _, lc := range l.Links {
			hostLinks[lc.WorkerHost] = append(hostLinks[lc.WorkerHost], lc)
		}

		l.State.Bridges = make(map[string]*BridgeState)
		statsPortNext := l.Config.LinkPortBase - 1

		for _, host := range sortedHosts(hostLinks) {
			statsPort := statsPortNext
			statsPortNext--

			links := hostLinks[host]

			// Determine bind and reachable addresses for the stats listener.
			// Local bridge binds to 127.0.0.1; remote binds to 0.0.0.0.
			bindAddr := "127.0.0.1"
			hostIP := resolveWorkerHostIP(host, l.Config)
			if host != "" {
				bindAddr = "0.0.0.0"
			}
			statsBindAddr := fmt.Sprintf("%s:%d", bindAddr, statsPort)

			if host == "" {
				// Local bridge
				if err := WriteBridgeConfig(l.StateDir, links, statsBindAddr); err != nil {
					return err
				}
				pid, err := startBridgeProcess(l.Name, l.StateDir)
				if err != nil {
					return fmt.Errorf("newtlab: start bridge: %w", err)
				}
				reachAddr := fmt.Sprintf("127.0.0.1:%d", statsPort)
				l.State.Bridges[""] = &BridgeState{PID: pid, StatsAddr: reachAddr}
				l.State.BridgePID = pid // back-compat
			} else {
				// Remote bridge
				cfg := buildBridgeConfig(links, statsBindAddr)
				configJSON, err := json.MarshalIndent(cfg, "", "    ")
				if err != nil {
					return fmt.Errorf("newtlab: marshal remote bridge config: %w", err)
				}
				pid, err := startBridgeProcessRemote(l.Name, hostIP, configJSON)
				if err != nil {
					return fmt.Errorf("newtlab: start remote bridge on %s: %w", hostIP, err)
				}
				reachAddr := fmt.Sprintf("%s:%d", hostIP, statsPort)
				l.State.Bridges[host] = &BridgeState{PID: pid, HostIP: hostIP, StatsAddr: reachAddr}
			}

			// Wait for bridge listeners to be ready
			for _, lc := range links {
				probeHost := "127.0.0.1"
				if host != "" {
					probeHost = hostIP
				}
				if err := waitForPort(probeHost, lc.APort, 10*time.Second); err != nil {
					return fmt.Errorf("newtlab: bridge not ready: %w", err)
				}
				if err := waitForPort(probeHost, lc.ZPort, 10*time.Second); err != nil {
					return fmt.Errorf("newtlab: bridge not ready: %w", err)
				}
			}
		}
	}

	// Start QEMU processes in sorted name order.
	// All VMs connect outbound to bridge workers — no ordering constraints.
	for _, name := range sortedNodeNames(l.Nodes) {
		node := l.Nodes[name]
		hostIP := resolveHostIP(node, l.Config)

		pid, err := StartNode(node, l.StateDir, hostIP)
		if err != nil {
			l.State.Nodes[name] = &NodeState{
				Status:      "error",
				SSHPort:     node.SSHPort,
				ConsolePort: node.ConsolePort,
				Host:        node.Host,
				HostIP:      hostIP,
			}
			deployErr = err
			continue
		}
		l.State.Nodes[name] = &NodeState{
			PID:         pid,
			Status:      "running",
			SSHPort:     node.SSHPort,
			ConsolePort: node.ConsolePort,
			Host:        node.Host,
			HostIP:      hostIP,
		}
	}

	// Bootstrap network via serial console (parallel).
	// QEMU user-mode networking requires eth0 up + DHCP before SSH port forwarding works.
	var wg sync.WaitGroup
	var mu sync.Mutex
	for name, node := range l.Nodes {
		ns := l.State.Nodes[name]
		if ns.Status != "running" {
			continue
		}
		consoleHost := "127.0.0.1"
		if ns.HostIP != "" {
			consoleHost = ns.HostIP
		}
		wg.Add(1)
		go func(name, consoleHost string, node *NodeConfig) {
			defer wg.Done()
			err := BootstrapNetwork(consoleHost, node.ConsolePort,
				node.ConsoleUser, node.ConsolePass, node.SSHUser, node.SSHPass,
				time.Duration(node.BootTimeout)*time.Second)
			if err != nil {
				mu.Lock()
				l.State.Nodes[name].Status = "error"
				if deployErr == nil {
					deployErr = err
				}
				mu.Unlock()
			}
		}(name, consoleHost, node)
	}
	wg.Wait()

	// Wait for SSH readiness (parallel)
	for name, node := range l.Nodes {
		ns := l.State.Nodes[name]
		if ns.Status != "running" {
			continue
		}
		sshHost := "127.0.0.1"
		if ns.HostIP != "" {
			sshHost = ns.HostIP
		}
		wg.Add(1)
		go func(name, sshHost string, node *NodeConfig) {
			defer wg.Done()
			err := WaitForSSH(sshHost, node.SSHPort, node.SSHUser, node.SSHPass,
				60*time.Second)
			if err != nil {
				mu.Lock()
				l.State.Nodes[name].Status = "error"
				if deployErr == nil {
					deployErr = err
				}
				mu.Unlock()
			}
		}(name, sshHost, node)
	}
	wg.Wait()

	// Apply platform boot patches (parallel, per node).
	// Resolve patches for each platform's dataplane + image release,
	// compute template vars from NodeConfig, apply via SSH.
	for name, node := range l.Nodes {
		ns := l.State.Nodes[name]
		if ns.Status != "running" {
			continue
		}
		profile := l.Profiles[name]
		platform := l.Platform.Platforms[profile.Platform]
		if platform == nil {
			continue
		}
		patches, err := ResolveBootPatches(platform.Dataplane, platform.VMImageRelease)
		if err != nil || len(patches) == 0 {
			continue
		}
		sshHost := "127.0.0.1"
		if ns.HostIP != "" {
			sshHost = ns.HostIP
		}
		vars := buildPatchVars(node, platform)
		wg.Add(1)
		go func(name, sshHost string, node *NodeConfig, patches []*BootPatch, vars *PatchVars) {
			defer wg.Done()
			err := ApplyBootPatches(sshHost, node.SSHPort, node.SSHUser, node.SSHPass, patches, vars)
			if err != nil {
				mu.Lock()
				if deployErr == nil {
					deployErr = fmt.Errorf("newtlab: boot patches %s: %w", name, err)
				}
				mu.Unlock()
			}
		}(name, sshHost, node, patches, vars)
	}
	wg.Wait()

	// Patch profiles
	if err := PatchProfiles(l); err != nil {
		if deployErr == nil {
			deployErr = err
		}
	}

	// Save state (always, even on error — enables cleanup)
	if err := SaveState(l.State); err != nil {
		return err
	}

	return deployErr
}

// Destroy kills QEMU processes, removes overlays, cleans state,
// and restores profiles.
func (l *Lab) Destroy() error {
	state, err := LoadState(l.Name)
	if err != nil {
		return err
	}
	l.State = state
	if l.SpecDir == "" {
		l.SpecDir = state.SpecDir
	}

	var errs []error

	// Kill QEMU processes
	for name, node := range state.Nodes {
		if IsRunning(node.PID, node.HostIP) {
			if err := StopNode(node.PID, node.HostIP); err != nil {
				errs = append(errs, fmt.Errorf("stop %s (pid %d): %w", name, node.PID, err))
			}
		}
	}

	// Stop bridge processes (per-host)
	if len(state.Bridges) > 0 {
		for host, bs := range state.Bridges {
			if bs.HostIP != "" {
				if err := stopBridgeProcessRemote(bs.PID, bs.HostIP); err != nil {
					errs = append(errs, fmt.Errorf("stop bridge on %s (pid %d): %w", host, bs.PID, err))
				}
			} else if isRunningLocal(bs.PID) {
				if err := stopNodeLocal(bs.PID); err != nil {
					errs = append(errs, fmt.Errorf("stop bridge (pid %d): %w", bs.PID, err))
				}
			}
		}
	} else if state.BridgePID > 0 && isRunningLocal(state.BridgePID) {
		// Legacy fallback
		if err := stopNodeLocal(state.BridgePID); err != nil {
			errs = append(errs, fmt.Errorf("stop bridge (pid %d): %w", state.BridgePID, err))
		}
	}

	// Clean up remote state directories
	cleanedHosts := map[string]bool{}
	for _, node := range state.Nodes {
		if node.HostIP != "" && !cleanedHosts[node.HostIP] {
			if err := cleanupRemoteStateDir(l.Name, node.HostIP); err != nil {
				errs = append(errs, fmt.Errorf("cleanup remote state on %s: %w", node.HostIP, err))
			}
			cleanedHosts[node.HostIP] = true
		}
	}

	// Restore profiles
	if err := RestoreProfiles(l); err != nil {
		errs = append(errs, fmt.Errorf("restore profiles: %w", err))
	}

	// Remove local state directory
	if err := RemoveState(l.Name); err != nil {
		errs = append(errs, fmt.Errorf("remove state: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("newtlab: destroy had %d errors: %v", len(errs), errs)
	}
	return nil
}

// Status returns the current state by loading state.json, then live-checking
// each PID via IsRunning() to update node status.
func (l *Lab) Status() (*LabState, error) {
	state, err := LoadState(l.Name)
	if err != nil {
		return nil, err
	}

	for _, node := range state.Nodes {
		if node.Status == "running" && !IsRunning(node.PID, node.HostIP) {
			node.Status = "stopped"
		}
	}

	return state, nil
}

// FilterHost removes nodes not assigned to the given host name.
// Nodes without a vm_host profile field are retained (single-host mode).
// Nodes matching the given host have their Host cleared so they are
// treated as local — QEMU runs directly instead of via SSH to self.
func (l *Lab) FilterHost(host string) {
	for name, node := range l.Nodes {
		if node.Host != "" && node.Host != host {
			delete(l.Nodes, name)
		} else if node.Host == host {
			node.Host = "" // this host is local — no SSH to self
		}
	}

	// Remove links that reference filtered-out nodes
	var filtered []*LinkConfig
	for _, link := range l.Links {
		_, aOK := l.Nodes[link.A.Device]
		_, zOK := l.Nodes[link.Z.Device]
		if aOK && zOK {
			filtered = append(filtered, link)
		}
	}
	l.Links = filtered
}

// Stop stops a single node by PID.
func (l *Lab) Stop(nodeName string) error {
	state, err := LoadState(l.Name)
	if err != nil {
		return err
	}

	node, ok := state.Nodes[nodeName]
	if !ok {
		return fmt.Errorf("newtlab: node %q not found", nodeName)
	}

	if err := StopNode(node.PID, node.HostIP); err != nil {
		return err
	}

	node.Status = "stopped"
	return SaveState(state)
}

// Start restarts a stopped node.
func (l *Lab) Start(nodeName string) error {
	state, err := LoadState(l.Name)
	if err != nil {
		return err
	}

	nodeState, ok := state.Nodes[nodeName]
	if !ok {
		return fmt.Errorf("newtlab: node %q not found", nodeName)
	}

	// Re-load lab config to rebuild the node
	lab, err := NewLab(state.SpecDir)
	if err != nil {
		return fmt.Errorf("newtlab: reload specs: %w", err)
	}

	node, ok := lab.Nodes[nodeName]
	if !ok {
		return fmt.Errorf("newtlab: node %q not found in specs", nodeName)
	}

	// Restore allocated ports from state
	node.SSHPort = nodeState.SSHPort
	node.ConsolePort = nodeState.ConsolePort

	pid, err := StartNode(node, LabDir(l.Name), nodeState.HostIP)
	if err != nil {
		return err
	}

	nodeState.PID = pid
	nodeState.Status = "running"

	// Wait for SSH — use host IP for remote nodes
	sshHost := "127.0.0.1"
	if nodeState.HostIP != "" {
		sshHost = nodeState.HostIP
	}
	if err := WaitForSSH(sshHost, node.SSHPort, node.SSHUser, node.SSHPass,
		time.Duration(node.BootTimeout)*time.Second); err != nil {
		nodeState.Status = "error"
		SaveState(state)
		return err
	}

	return SaveState(state)
}

// Provision runs newtron provisioning for all (or specified) devices.
func (l *Lab) Provision(parallel int) error {
	state, err := LoadState(l.Name)
	if err != nil {
		return err
	}
	if l.SpecDir == "" {
		l.SpecDir = state.SpecDir
	}

	sem := make(chan struct{}, parallel)
	var mu sync.Mutex
	var errs []error

	var wg sync.WaitGroup
	for name := range state.Nodes {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cmd := exec.Command("newtron", "provision", "-S", l.SpecDir, "-d", name, "-xs")
			output, err := cmd.CombinedOutput()
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("provision %s: %w\n%s", name, err, output))
				mu.Unlock()
			}
		}(name)
	}
	wg.Wait()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// destroyExisting tears down a stale deployment found via state.json.
func (l *Lab) destroyExisting(existing *LabState) error {
	old := &Lab{
		Name:    l.Name,
		SpecDir: existing.SpecDir,
		State:   existing,
	}
	// Kill QEMU processes
	for name, node := range existing.Nodes {
		if IsRunning(node.PID, node.HostIP) {
			if err := StopNode(node.PID, node.HostIP); err != nil {
				fmt.Fprintf(os.Stderr, "warning: stop %s (pid %d): %v\n", name, node.PID, err)
			}
		}
	}
	// Kill bridge processes (per-host)
	if len(existing.Bridges) > 0 {
		for _, bs := range existing.Bridges {
			if bs.HostIP != "" {
				stopBridgeProcessRemote(bs.PID, bs.HostIP)
			} else if isRunningLocal(bs.PID) {
				stopNodeLocal(bs.PID)
			}
		}
	} else if existing.BridgePID > 0 && isRunningLocal(existing.BridgePID) {
		stopNodeLocal(existing.BridgePID)
	}
	// Clean up remote state directories
	cleanedHosts := map[string]bool{}
	for _, node := range existing.Nodes {
		if node.HostIP != "" && !cleanedHosts[node.HostIP] {
			cleanupRemoteStateDir(l.Name, node.HostIP) // best effort
			cleanedHosts[node.HostIP] = true
		}
	}
	// Restore profiles (best effort)
	RestoreProfiles(old)
	// Remove local state
	return RemoveState(l.Name)
}

// resolveNewtLabConfig returns a VMLabConfig with defaults applied.
func resolveNewtLabConfig(cfg *spec.NewtLabConfig) *VMLabConfig {
	resolved := &VMLabConfig{
		LinkPortBase:    20000,
		ConsolePortBase: 30000,
		SSHPortBase:     40000,
	}
	if cfg != nil {
		if cfg.LinkPortBase != 0 {
			resolved.LinkPortBase = cfg.LinkPortBase
		}
		if cfg.ConsolePortBase != 0 {
			resolved.ConsolePortBase = cfg.ConsolePortBase
		}
		if cfg.SSHPortBase != 0 {
			resolved.SSHPortBase = cfg.SSHPortBase
		}
		if len(cfg.Servers) > 0 {
			resolved.Servers = cfg.Servers
			resolved.Hosts = make(map[string]string, len(cfg.Servers))
			for _, s := range cfg.Servers {
				resolved.Hosts[s.Name] = s.Address
			}
		} else {
			resolved.Hosts = cfg.Hosts
		}
	}
	return resolved
}

// resolveHostIP returns the IP address for a node's host.
// For local nodes (Host==""), returns "" (callers default to 127.0.0.1).
// For remote nodes, looks up the host name in the lab's Hosts map.
func resolveHostIP(node *NodeConfig, config *VMLabConfig) string {
	if node.Host == "" {
		return ""
	}
	if config != nil && config.Hosts != nil {
		if ip, ok := config.Hosts[node.Host]; ok {
			return ip
		}
	}
	// Fall back to host name as IP (allows using raw IPs as host names)
	return node.Host
}

// resolveWorkerHostIP returns the IP address for a bridge worker's host.
// For the local host (host==""), returns "127.0.0.1".
// For remote hosts, looks up the host name in the lab's Hosts map.
func resolveWorkerHostIP(host string, config *VMLabConfig) string {
	if host == "" {
		return "127.0.0.1"
	}
	if config != nil && config.Hosts != nil {
		if ip, ok := config.Hosts[host]; ok {
			return ip
		}
	}
	return host
}

// sortedHosts returns host keys from the map in sorted order,
// with the local host ("") always first.
func sortedHosts(hostLinks map[string][]*LinkConfig) []string {
	hosts := make([]string, 0, len(hostLinks))
	for host := range hostLinks {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}
