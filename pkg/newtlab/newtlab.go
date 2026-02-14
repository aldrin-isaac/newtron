package newtlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// Lab is the top-level newtlab orchestrator. It reads newtron spec files,
// resolves VM configuration, and manages QEMU processes.
type Lab struct {
	Name         string
	SpecDir      string
	StateDir     string
	Topology     *spec.TopologySpecFile
	Platform     *spec.PlatformSpecFile
	Profiles     map[string]*spec.DeviceProfile
	Config       *VMLabConfig
	Nodes        map[string]*NodeConfig
	Links        []*LinkConfig
	State        *LabState
	Force        bool
	DeviceFilter []string // if non-empty, only provision these devices
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
// and patches profiles. The context is checked at each major phase and
// threaded into long-running sub-operations.
func (l *Lab) Deploy(ctx context.Context) error {
	util.Logger.Infof("newtlab: deploying lab %s from %s", l.Name, l.SpecDir)

	// Check for stale state
	if existing, err := LoadState(l.Name); err == nil && existing != nil {
		if !l.Force {
			return fmt.Errorf("newtlab: lab %s already deployed (created %s); use --force to redeploy",
				l.Name, existing.Created.Format(time.RFC3339))
		}
		if err := l.destroyExisting(existing); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cleanup of existing lab failed: %v\n", err)
		}
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
		if node.Host != "" {
			hostIP := resolveHostIP(node.Host, l.Config)
			if !remoteHosts[hostIP] {
				if err := setupRemoteStateDir(l.Name, hostIP); err != nil {
					return err
				}
				remoteHosts[hostIP] = true
			}
		}
	}

	// Create overlay disks (local or remote)
	for name, node := range l.Nodes {
		if node.Host != "" {
			// Remote: use ~/-based paths for shell expansion on remote host
			hostIP := resolveHostIP(node.Host, l.Config)
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

	if err := l.setupBridges(ctx); err != nil {
		return err
	}

	// Check for cancellation before starting VMs
	select {
	case <-ctx.Done():
		SaveState(l.State)
		return fmt.Errorf("newtlab: deploy cancelled: %w", ctx.Err())
	default:
	}

	var deployErr error
	if err := l.startNodes(ctx); err != nil {
		deployErr = err
	}
	if err := l.bootstrapNodes(ctx); err != nil && deployErr == nil {
		deployErr = err
	}
	if err := l.applyNodePatches(ctx); err != nil && deployErr == nil {
		deployErr = err
	}

	// Save state (always, even on error — enables cleanup)
	if err := SaveState(l.State); err != nil {
		return err
	}

	return deployErr
}

// setupBridges starts bridge worker processes for inter-VM networking.
// Groups links by host and starts a local or remote bridge per host.
func (l *Lab) setupBridges(ctx context.Context) error {
	if len(l.Links) == 0 {
		return nil
	}

	hostLinks := map[string][]*LinkConfig{}
	for _, lc := range l.Links {
		hostLinks[lc.WorkerHost] = append(hostLinks[lc.WorkerHost], lc)
	}

	l.State.Bridges = make(map[string]*BridgeState)
	statsPorts := allocateBridgeStatsPorts(l)

	for _, host := range sortedHosts(hostLinks) {
		statsPort := statsPorts[host]
		links := hostLinks[host]

		bindAddr := "127.0.0.1"
		hostIP := resolveHostIP(host, l.Config)
		if host != "" {
			bindAddr = "0.0.0.0"
		}
		statsBindAddr := fmt.Sprintf("%s:%d", bindAddr, statsPort)

		if host == "" {
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

	return nil
}

// startNodes starts QEMU processes for all nodes in sorted order.
// Continues on error so that remaining nodes are still started.
func (l *Lab) startNodes(ctx context.Context) error {
	var firstErr error
	for _, name := range sortedNodeNames(l.Nodes) {
		node := l.Nodes[name]
		hostIP := resolveHostIP(node.Host, l.Config)

		// StartNode/StopNode/IsRunning use "" to detect local vs remote
		remoteIP := ""
		if node.Host != "" {
			remoteIP = hostIP
		}

		util.WithDevice(name).Infof("newtlab: starting VM (ssh=%d, console=%d)", node.SSHPort, node.ConsolePort)
		pid, err := StartNode(node, l.StateDir, remoteIP)
		if err != nil {
			l.State.Nodes[name] = &NodeState{
				Status:      "error",
				SSHPort:     node.SSHPort,
				ConsolePort: node.ConsolePort,
				Host:        node.Host,
				HostIP:      remoteIP,
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		l.State.Nodes[name] = &NodeState{
			PID:         pid,
			Status:      "running",
			SSHPort:     node.SSHPort,
			ConsolePort: node.ConsolePort,
			Host:        node.Host,
			HostIP:      remoteIP,
		}
	}
	return firstErr
}

// parallelForNodes runs fn for each node with status "running" in parallel.
// On error, the node's status is set to "error" and the first error is returned.
func (l *Lab) parallelForNodes(fn func(name string, node *NodeConfig, ns *NodeState) error) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for name, node := range l.Nodes {
		ns := l.State.Nodes[name]
		if ns == nil || ns.Status != "running" {
			continue
		}
		wg.Add(1)
		go func(name string, node *NodeConfig, ns *NodeState) {
			defer wg.Done()
			if err := fn(name, node, ns); err != nil {
				mu.Lock()
				ns.Status = "error"
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(name, node, ns)
	}
	wg.Wait()
	return firstErr
}

// bootstrapNodes configures network via serial console and waits for SSH (parallel).
func (l *Lab) bootstrapNodes(ctx context.Context) error {
	// Bootstrap network via serial console (parallel).
	// QEMU user-mode networking requires eth0 up + DHCP before SSH port forwarding works.
	err := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		consoleHost := resolveHostIP(node.Host, l.Config)
		return BootstrapNetwork(ctx, consoleHost, node.ConsolePort,
			node.ConsoleUser, node.ConsolePass, node.SSHUser, node.SSHPass,
			time.Duration(node.BootTimeout)*time.Second)
	})

	// Wait for SSH readiness (parallel) — only for nodes still running.
	if err2 := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		sshHost := resolveHostIP(node.Host, l.Config)
		return WaitForSSH(ctx, sshHost, node.SSHPort, node.SSHUser, node.SSHPass,
			60*time.Second)
	}); err == nil {
		err = err2
	}

	return err
}

// applyNodePatches resolves and applies platform boot patches per node (parallel),
// then patches device profiles.
func (l *Lab) applyNodePatches(ctx context.Context) error {
	err := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		profile := l.Profiles[name]
		platform := l.Platform.Platforms[profile.Platform]
		if platform == nil {
			return nil
		}
		patches, patchErr := ResolveBootPatches(platform.Dataplane, platform.VMImageRelease)
		if patchErr != nil || len(patches) == 0 {
			return nil
		}
		sshHost := resolveHostIP(node.Host, l.Config)
		vars := buildPatchVars(node, platform)
		if patchErr = ApplyBootPatches(ctx, sshHost, node.SSHPort, node.SSHUser, node.SSHPass, patches, vars); patchErr != nil {
			return fmt.Errorf("newtlab: boot patches %s: %w", name, patchErr)
		}
		return nil
	})

	if patchErr := PatchProfiles(l); patchErr != nil {
		if err == nil {
			err = patchErr
		}
	}

	return err
}

// DestroyByName destroys a lab identified only by name, loading all
// necessary state from disk. This avoids constructing a partial Lab struct.
func DestroyByName(ctx context.Context, name string) error {
	lab := &Lab{Name: name}
	return lab.Destroy(ctx)
}

// StopByName stops a node in a lab identified only by lab name and node name,
// loading all necessary state from disk.
func StopByName(ctx context.Context, labName, nodeName string) error {
	lab := &Lab{Name: labName}
	return lab.Stop(ctx, nodeName)
}

// StartByName starts a node in a lab identified only by lab name and node name,
// loading all necessary state from disk.
func StartByName(ctx context.Context, labName, nodeName string) error {
	lab := &Lab{Name: labName}
	return lab.Start(ctx, nodeName)
}

// Destroy kills QEMU processes, removes overlays, cleans state,
// and restores profiles. The context allows cancellation of the
// teardown sequence.
func (l *Lab) Destroy(ctx context.Context) error {
	util.Logger.Infof("newtlab: destroying lab %s", l.Name)

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
	errs = append(errs, stopAllBridges(state)...)

	// Clean up remote state directories
	errs = append(errs, cleanupAllRemoteHosts(l.Name, state)...)

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
func (l *Lab) Stop(ctx context.Context, nodeName string) error {
	util.WithDevice(nodeName).Infof("newtlab: stopping node")
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
func (l *Lab) Start(ctx context.Context, nodeName string) error {
	util.WithDevice(nodeName).Infof("newtlab: starting node")
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

	// Wait for SSH — resolve host IP for the node
	sshHost := resolveHostIP(node.Host, lab.Config)
	if err := WaitForSSH(ctx, sshHost, node.SSHPort, node.SSHUser, node.SSHPass,
		time.Duration(node.BootTimeout)*time.Second); err != nil {
		nodeState.Status = "error"
		SaveState(state)
		return err
	}

	return SaveState(state)
}

// Provision runs newtron provisioning for all (or specified) devices.
// The context is threaded into exec.CommandContext for cancellation.
func (l *Lab) Provision(ctx context.Context, parallel int) error {
	util.Logger.Infof("newtlab: provisioning lab %s (parallel=%d)", l.Name, parallel)
	state, err := LoadState(l.Name)
	if err != nil {
		return err
	}
	if l.SpecDir == "" {
		l.SpecDir = state.SpecDir
	}

	// Apply device filter if set
	if len(l.DeviceFilter) > 0 {
		allowed := make(map[string]bool, len(l.DeviceFilter))
		for _, d := range l.DeviceFilter {
			allowed[d] = true
		}
		for name := range state.Nodes {
			if !allowed[name] {
				delete(state.Nodes, name)
			}
		}
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

			cmd := exec.CommandContext(ctx, findSiblingBinary("newtron"), "provision", "-S", l.SpecDir, "-d", name, "-xs")
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

	// Post-provision: trigger a global BGP soft clear to force route
	// re-advertisement. When devices are provisioned in parallel, each
	// device's ApplyFRRDefaults runs before all peers are ready, so routes
	// may remain un-advertised until the next timer cycle (120s). A single
	// soft clear pass after all devices are provisioned resolves this.
	l.refreshBGP(state)

	return nil
}

// refreshBGP SSHs to each device and runs "clear bgp * soft" via vtysh.
// Errors are logged but not returned — this is a best-effort convergence aid.
// SSH credentials are read from l.Nodes (already resolved during NewLab).
// bgpRefreshDelay is the wait time after provisioning before issuing
// "clear bgp * soft" to allow the last device's BGP session to initialize.
const bgpRefreshDelay = 5 * time.Second

func (l *Lab) refreshBGP(state *LabState) {
	// Brief delay for the last-provisioned device's BGP to start.
	time.Sleep(bgpRefreshDelay)

	for name, nodeState := range state.Nodes {
		nc := l.Nodes[name]
		if nc == nil || nc.SSHUser == "" {
			continue
		}

		host := resolveHostIP(nc.Host, l.Config)
		addr := fmt.Sprintf("%s:%d", host, nodeState.SSHPort)

		config := &ssh.ClientConfig{
			User:            nc.SSHUser,
			Auth:            []ssh.AuthMethod{ssh.Password(nc.SSHPass)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		}

		client, err := ssh.Dial("tcp", addr, config)
		if err != nil {
			util.Logger.Warnf("refreshBGP %s: SSH dial: %v", name, err)
			continue
		}
		session, err := client.NewSession()
		if err != nil {
			util.Logger.Warnf("refreshBGP %s: new session: %v", name, err)
			client.Close()
			continue
		}
		if err := session.Run("vtysh -c 'clear bgp * soft'"); err != nil {
			util.Logger.Warnf("refreshBGP %s: clear bgp: %v", name, err)
		} else {
			util.Logger.Infof("refreshBGP %s: done", name)
		}
		session.Close()
		client.Close()
	}
}

// findSiblingBinary returns the path to a binary adjacent to the current
// executable. Falls back to the bare name (resolved via $PATH).
func findSiblingBinary(name string) string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return name
}

// stopAllBridges stops all bridge processes tracked in a lab state.
// Returns a list of errors encountered during shutdown.
func stopAllBridges(state *LabState) []error {
	var errs []error
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
	return errs
}

// cleanupAllRemoteHosts removes remote state directories for all unique
// remote hosts referenced in the lab state. Returns a list of errors.
func cleanupAllRemoteHosts(labName string, state *LabState) []error {
	var errs []error
	cleaned := map[string]bool{}
	for _, node := range state.Nodes {
		if node.HostIP != "" && !cleaned[node.HostIP] {
			if err := cleanupRemoteStateDir(labName, node.HostIP); err != nil {
				errs = append(errs, fmt.Errorf("cleanup remote state on %s: %w", node.HostIP, err))
			}
			cleaned[node.HostIP] = true
		}
	}
	return errs
}

// destroyExisting tears down a stale deployment found via state.json.
// Collects all errors instead of logging to stderr.
func (l *Lab) destroyExisting(existing *LabState) error {
	old := &Lab{
		Name:    l.Name,
		SpecDir: existing.SpecDir,
		State:   existing,
	}
	var errs []error

	// Kill QEMU processes
	for name, node := range existing.Nodes {
		if IsRunning(node.PID, node.HostIP) {
			if err := StopNode(node.PID, node.HostIP); err != nil {
				errs = append(errs, fmt.Errorf("stop %s (pid %d): %w", name, node.PID, err))
			}
		}
	}

	// Kill bridge processes (per-host)
	errs = append(errs, stopAllBridges(existing)...)

	// Clean up remote state directories
	errs = append(errs, cleanupAllRemoteHosts(l.Name, existing)...)

	// Restore profiles
	if err := RestoreProfiles(old); err != nil {
		errs = append(errs, fmt.Errorf("restore profiles: %w", err))
	}

	// Remove local state
	if err := RemoveState(l.Name); err != nil {
		errs = append(errs, fmt.Errorf("remove state: %w", err))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
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

// resolveHostIP returns a usable IP address for a host name.
// For local hosts (host==""), returns "127.0.0.1".
// For remote hosts, looks up the host name in the lab's Hosts map,
// falling back to the host name itself (allows raw IPs as host names).
func resolveHostIP(host string, config *VMLabConfig) string {
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
