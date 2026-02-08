package newtlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

		// Expand ~ in image path
		if strings.HasPrefix(nc.Image, "~/") {
			home, _ := os.UserHomeDir()
			nc.Image = filepath.Join(home, nc.Image[2:])
		}

		l.Nodes[name] = nc
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

	// Port conflict detection
	for _, node := range l.Nodes {
		for _, port := range []int{node.SSHPort, node.ConsolePort} {
			if err := probePort(port); err != nil {
				return fmt.Errorf("newtlab: %w", err)
			}
		}
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
			A:    fmt.Sprintf("%s:%s", lc.A.Device, lc.A.Interface),
			Z:    fmt.Sprintf("%s:%s", lc.Z.Device, lc.Z.Interface),
			Port: lc.Port,
		})
	}

	// Create overlay disks
	for name, node := range l.Nodes {
		overlayPath := filepath.Join(l.StateDir, "disks", name+".qcow2")
		if err := CreateOverlay(node.Image, overlayPath); err != nil {
			return err
		}
	}

	// Start QEMU processes: listen-side first, then connect-side
	var deployErr error
	for _, name := range sortedListenFirst(l.Nodes, l.Links) {
		node := l.Nodes[name]
		pid, err := StartNode(node, l.StateDir)
		if err != nil {
			l.State.Nodes[name] = &NodeState{
				Status:      "error",
				SSHPort:     node.SSHPort,
				ConsolePort: node.ConsolePort,
			}
			deployErr = err
			continue
		}
		l.State.Nodes[name] = &NodeState{
			PID:         pid,
			Status:      "running",
			SSHPort:     node.SSHPort,
			ConsolePort: node.ConsolePort,
		}
	}

	// Wait for SSH readiness (parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for name, node := range l.Nodes {
		if l.State.Nodes[name].Status != "running" {
			continue
		}
		wg.Add(1)
		go func(name string, node *NodeConfig) {
			defer wg.Done()
			err := WaitForSSH("127.0.0.1", node.SSHPort, node.SSHUser, node.SSHPass,
				time.Duration(node.BootTimeout)*time.Second)
			if err != nil {
				mu.Lock()
				l.State.Nodes[name].Status = "error"
				if deployErr == nil {
					deployErr = err
				}
				mu.Unlock()
			}
		}(name, node)
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
		if IsRunning(node.PID) {
			if err := StopNode(node.PID); err != nil {
				errs = append(errs, fmt.Errorf("stop %s (pid %d): %w", name, node.PID, err))
			}
		}
	}

	// Restore profiles
	if err := RestoreProfiles(l); err != nil {
		errs = append(errs, fmt.Errorf("restore profiles: %w", err))
	}

	// Remove state directory
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
		if node.Status == "running" && !IsRunning(node.PID) {
			node.Status = "stopped"
		}
	}

	return state, nil
}

// FilterHost removes nodes not assigned to the given host name.
// Nodes without a vm_host profile field are retained (single-host mode).
func (l *Lab) FilterHost(host string) {
	for name, node := range l.Nodes {
		if node.Host != "" && node.Host != host {
			delete(l.Nodes, name)
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

	if err := StopNode(node.PID); err != nil {
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

	pid, err := StartNode(node, LabDir(l.Name))
	if err != nil {
		return err
	}

	nodeState.PID = pid
	nodeState.Status = "running"

	// Wait for SSH
	if err := WaitForSSH("127.0.0.1", node.SSHPort, node.SSHUser, node.SSHPass,
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

			cmd := exec.Command("newtron", "provision", "-S", l.SpecDir, "-d", name, "-x")
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
	// Kill processes
	for name, node := range existing.Nodes {
		if IsRunning(node.PID) {
			if err := StopNode(node.PID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: stop %s (pid %d): %v\n", name, node.PID, err)
			}
		}
	}
	// Restore profiles (best effort)
	RestoreProfiles(old)
	// Remove state
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
		resolved.Hosts = cfg.Hosts
	}
	return resolved
}

// sortedListenFirst returns node names sorted so that A-side (listen) nodes
// come before Z-side (connect-only) nodes. Within each group, alphabetical.
func sortedListenFirst(nodes map[string]*NodeConfig, links []*LinkConfig) []string {
	listenNodes := make(map[string]bool)
	for _, link := range links {
		listenNodes[link.A.Device] = true
	}

	var listen, connect []string
	for name := range nodes {
		if listenNodes[name] {
			listen = append(listen, name)
		} else {
			connect = append(connect, name)
		}
	}

	sort.Strings(listen)
	sort.Strings(connect)
	return append(listen, connect...)
}
