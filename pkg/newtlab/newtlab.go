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
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// HostVMGroup represents virtual hosts coalesced into one VM.
type HostVMGroup struct {
	VMName  string         // synthetic VM name (e.g., "hostvm-0")
	Hosts   []string       // sorted logical host names
	NICBase map[string]int // host name → base NIC index on VM
}

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
	HostVMs      []*HostVMGroup
	Force        bool
	DeviceFilter []string // if non-empty, only provision these devices

	// OnProgress is an optional callback for reporting deploy/destroy progress.
	OnProgress func(phase, detail string)
}

func (l *Lab) progress(phase, detail string) {
	if l.OnProgress != nil {
		l.OnProgress(phase, detail)
	}
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

	// Derive links from interface.link fields if not explicitly provided
	if len(l.Topology.Links) == 0 {
		l.Topology.Links = spec.DeriveLinksFromInterfaces(l.Topology)
		util.Logger.Infof("newtlab: derived %d links from interface.link fields", len(l.Topology.Links))
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
		platformName := profile.Platform
		if platformName == "" {
			// Fallback to topology default platform
			platformName = l.Topology.Platform
		}
		if platformName != "" {
			platform = l.Platform.Platforms[platformName]
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

	// Coalesce host devices into shared VMs
	l.coalesceHostVMs()

	// Auto-place nodes across server pool (if configured)
	if len(l.Config.Servers) > 0 {
		if err := PlaceNodes(l.Nodes, l.Config.Servers); err != nil {
			return nil, err
		}
	}

	// Build host map for link allocation
	hostMap := l.buildHostMap()

	// Allocate links (topology.Links populated by loader from interface.link fields)
	l.Links, err = AllocateLinks(l.Topology.Links, l.Nodes, l.Config, hostMap)
	if err != nil {
		return nil, err
	}

	return l, nil
}

// coalesceHostVMs identifies host devices in the topology, groups them by
// vm_host (physical server), creates a synthetic VM per group, and removes
// individual host NodeConfigs from l.Nodes.
func (l *Lab) coalesceHostVMs() {
	// Identify host devices
	type hostInfo struct {
		name    string
		vmHost  string
		profile *spec.DeviceProfile
	}
	var hosts []hostInfo
	for name, nc := range l.Nodes {
		if nc.DeviceType == "host" {
			profile := l.Profiles[name]
			vmHost := ""
			if profile != nil {
				vmHost = profile.VMHost
			}
			hosts = append(hosts, hostInfo{name: name, vmHost: vmHost, profile: profile})
		}
	}
	if len(hosts) == 0 {
		return
	}

	// Sort hosts for deterministic ordering
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].name < hosts[j].name })

	// Group by vm_host
	groups := map[string][]hostInfo{}
	for _, h := range hosts {
		groups[h.vmHost] = append(groups[h.vmHost], h)
	}

	// Sort group keys for deterministic VM naming
	var groupKeys []string
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)

	vmIndex := 0
	for _, key := range groupKeys {
		groupHosts := groups[key]
		vmName := fmt.Sprintf("hostvm-%d", vmIndex)
		vmIndex++

		// Use the first host's NodeConfig as template for the VM
		templateNC := l.Nodes[groupHosts[0].name]

		vmNC := &NodeConfig{
			Name:         vmName,
			Platform:     templateNC.Platform,
			DeviceType:   "host-vm",
			Image:        templateNC.Image,
			Memory:       templateNC.Memory,
			CPUs:         templateNC.CPUs,
			NICDriver:    templateNC.NICDriver,
			InterfaceMap: templateNC.InterfaceMap,
			CPUFeatures:  templateNC.CPUFeatures,
			SSHUser:      templateNC.SSHUser,
			SSHPass:      templateNC.SSHPass,
			ConsoleUser:  templateNC.ConsoleUser,
			ConsolePass:  templateNC.ConsolePass,
			BootTimeout:  templateNC.BootTimeout,
			Host:         templateNC.Host,
			SSHPort:      templateNC.SSHPort,
			ConsolePort:  templateNC.ConsolePort,
			NICs: []NICConfig{{
				Index:     0,
				NetdevID:  "mgmt",
				Interface: "mgmt",
				MAC:       GenerateMAC(vmName, 0),
			}},
		}

		group := &HostVMGroup{
			VMName:  vmName,
			Hosts:   make([]string, 0, len(groupHosts)),
			NICBase: make(map[string]int),
		}

		// Compute NIC base per host: count links from topology, allocate sequential ranges
		nicIdx := 1 // NIC 0 = mgmt
		for _, h := range groupHosts {
			group.Hosts = append(group.Hosts, h.name)
			group.NICBase[h.name] = nicIdx

			// Count how many links this host has
			linkCount := 0
			for _, link := range l.Topology.Links {
				aDevice, _, _ := splitLinkEndpoint(link.A)
				zDevice, _, _ := splitLinkEndpoint(link.Z)
				if aDevice == h.name || zDevice == h.name {
					linkCount++
				}
			}
			nicIdx += linkCount
		}

		// Remove individual host NodeConfigs, add VM NodeConfig
		for _, h := range groupHosts {
			delete(l.Nodes, h.name)
		}
		l.Nodes[vmName] = vmNC
		l.HostVMs = append(l.HostVMs, group)
	}
}

// buildHostMap returns a mapping from coalesced host names to their VM name
// and NIC base index, for use by AllocateLinks.
func (l *Lab) buildHostMap() map[string]HostMapping {
	hostMap := make(map[string]HostMapping)
	for _, group := range l.HostVMs {
		for _, hostName := range group.Hosts {
			hostMap[hostName] = HostMapping{
				VMName:  group.VMName,
				NICBase: group.NICBase[hostName],
			}
		}
	}
	return hostMap
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

	// Generate a lab-specific Ed25519 key for passwordless SSH access.
	sshKeyPath, sshPubKey, keyErr := GenerateLabSSHKey(l.StateDir)
	if keyErr != nil {
		util.Logger.Warnf("newtlab: failed to generate lab SSH key: %v; falling back to password auth", keyErr)
		sshKeyPath, sshPubKey = "", ""
	}

	// Initialize state
	l.State = &LabState{
		Name:       l.Name,
		Created:    time.Now(),
		SpecDir:    l.SpecDir,
		SSHKeyPath: sshKeyPath,
		Nodes:      make(map[string]*NodeState),
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

	// Save initial state immediately to prevent stale links showing in status
	if err := SaveState(l.State); err != nil {
		return fmt.Errorf("newtlab: save initial state: %w", err)
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

	l.progress("bridges", fmt.Sprintf("starting %d bridge workers", len(l.Links)))
	if err := l.setupBridges(ctx); err != nil {
		return err
	}
	SaveState(l.State)

	// Check for cancellation before starting VMs
	select {
	case <-ctx.Done():
		SaveState(l.State)
		return fmt.Errorf("newtlab: deploy cancelled: %w", ctx.Err())
	default:
	}

	l.progress("start", fmt.Sprintf("booting %d VMs", len(l.Nodes)))
	var deployErr error
	if err := l.startNodes(ctx); err != nil {
		deployErr = err
	}

	l.progress("bootstrap", fmt.Sprintf("configuring %d nodes via serial", len(l.Nodes)))
	l.setNodePhase("bootstrapping")
	SaveState(l.State)
	if err := l.bootstrapNodes(ctx, sshPubKey); err != nil && deployErr == nil {
		deployErr = err
	}
	l.setNodePhase("")
	SaveState(l.State)

	l.progress("patch", "applying boot patches")
	l.setNodePhase("patching")
	SaveState(l.State)
	if err := l.applyNodePatches(ctx); err != nil && deployErr == nil {
		deployErr = err
	}
	l.setNodePhase("")

	// Provision host namespaces for coalesced VMs
	if len(l.HostVMs) > 0 && deployErr == nil {
		l.progress("hosts", "provisioning host namespaces")
		if err := l.provisionHostNamespaces(ctx); err != nil {
			deployErr = err
		}
	}

	// Create virtual host state entries
	for _, group := range l.HostVMs {
		vmState := l.State.Nodes[group.VMName]
		if vmState == nil {
			continue
		}
		vmNC := l.Nodes[group.VMName]
		for _, hostName := range group.Hosts {
			l.State.Nodes[hostName] = &NodeState{
				PID:         vmState.PID,
				Status:      vmState.Status,
				DeviceType:  "host",
				Image:       vmNC.Image,
				SSHPort:     vmState.SSHPort,
				ConsolePort: vmState.ConsolePort,
				Host:        vmState.Host,
				HostIP:      vmState.HostIP,
				SSHUser:     vmNC.SSHUser,
				VMName:      group.VMName,
				Namespace:   hostName,
			}
		}
	}

	// Save state (always, even on error — enables cleanup)
	if err := SaveState(l.State); err != nil {
		return err
	}

	l.progress("ready", "all nodes ready")
	return deployErr
}

// provisionHostNamespaces creates network namespaces inside coalesced host VMs
// and configures interfaces and IP addresses.
func (l *Lab) provisionHostNamespaces(ctx context.Context) error {
	for _, group := range l.HostVMs {
		vmNC := l.Nodes[group.VMName]
		vmNS := l.State.Nodes[group.VMName]
		if vmNS == nil || vmNS.Status != "running" {
			continue
		}

		sshHost := resolveHostIP(vmNC.Host, l.Config)
		config := &ssh.ClientConfig{
			User:            vmNC.SSHUser,
			Auth:            []ssh.AuthMethod{ssh.Password(vmNC.SSHPass)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         10 * time.Second,
		}
		addr := fmt.Sprintf("%s:%d", sshHost, vmNS.SSHPort)
		client, err := ssh.Dial("tcp", addr, config)
		if err != nil {
			return fmt.Errorf("newtlab: provision hosts: SSH dial %s: %w", addr, err)
		}
		defer client.Close()

		for hostIdx, hostName := range group.Hosts {
			nicBase := group.NICBase[hostName]
			profile := l.Profiles[hostName]

			var cmds []string
			cmds = append(cmds, fmt.Sprintf("ip netns add %s", hostName))

			// Move each data NIC into the namespace
			// For a host with one link (eth0), NIC = nicBase + 0
			// The VM sees this as eth<nicBase>
			ethName := fmt.Sprintf("eth%d", nicBase)
			cmds = append(cmds,
				fmt.Sprintf("ip link set %s netns %s", ethName, hostName),
				fmt.Sprintf("ip netns exec %s ip link set %s name eth0", hostName, ethName),
				fmt.Sprintf("ip netns exec %s ip link set eth0 up", hostName),
				fmt.Sprintf("ip netns exec %s ip link set lo up", hostName),
			)

			// Assign IP and gateway
			hostIP, gateway := "", ""
			if profile != nil {
				hostIP = profile.HostIP
				gateway = profile.HostGateway
			}
			if hostIP == "" {
				// Auto-derive from switch-side interface IP
				switchIP, mask := l.findPeerInterfaceIP(hostName)
				if switchIP != "" {
					gateway = switchIP
					hostIP = deriveHostIP(switchIP, mask, hostIdx)
				}
			}
			if hostIP != "" {
				cmds = append(cmds, fmt.Sprintf("ip netns exec %s ip addr add %s dev eth0", hostName, hostIP))
			}
			if gateway != "" {
				cmds = append(cmds, fmt.Sprintf("ip netns exec %s ip route add default via %s", hostName, gateway))
			}

			script := strings.Join(cmds, " && ")
			session, err := client.NewSession()
			if err != nil {
				return fmt.Errorf("newtlab: provision %s: SSH session: %w", hostName, err)
			}
			output, err := session.CombinedOutput(script)
			session.Close()
			if err != nil {
				return fmt.Errorf("newtlab: provision %s: %w\n%s", hostName, err, output)
			}
			util.Logger.Infof("newtlab: provisioned namespace %s in %s", hostName, group.VMName)
		}
	}
	return nil
}

// findPeerInterfaceIP finds the peer interface's IP for a given host device.
// Searches all devices for an interface with .link pointing to this host.
// Returns the peer IP (without mask) and the mask (e.g., "/24"), or empty strings if not found.
func (l *Lab) findPeerInterfaceIP(hostName string) (string, string) {
	// Search all devices for interfaces that link to this host
	for _, device := range l.Topology.Devices {
		for _, iface := range device.Interfaces {
			if iface.Link == "" {
				continue
			}

			// Check if this interface links to the target host
			// Format: "hostName:ethX"
			peerDevice, _, err := splitLinkEndpoint(iface.Link)
			if err != nil || peerDevice != hostName {
				continue
			}

			// Found the peer - return its IP
			if iface.IP == "" {
				continue
			}

			// Parse "10.1.100.1/24" → ip="10.1.100.1", mask="/24"
			parts := strings.SplitN(iface.IP, "/", 2)
			if len(parts) == 2 {
				return parts[0], "/" + parts[1]
			}
			return iface.IP, ""
		}
	}
	return "", ""
}

// deriveHostIP derives a host IP from the switch-side IP.
// For /30 and /31 (point-to-point): increments by (hostIndex + 1)
// For /24 and larger: hostIndex 0 → .10, 1 → .20, etc.
func deriveHostIP(switchIP, mask string, hostIndex int) string {
	parts := strings.Split(switchIP, ".")
	if len(parts) != 4 {
		return ""
	}

	// Parse prefix length from mask (e.g., "/30")
	prefixLen := 24 // default to /24 behavior
	if len(mask) > 1 && mask[0] == '/' {
		if n, err := fmt.Sscanf(mask[1:], "%d", &prefixLen); n != 1 || err != nil {
			prefixLen = 24
		}
	}

	lastOctet := 0
	fmt.Sscanf(parts[3], "%d", &lastOctet)

	// For point-to-point links, derive peer IP based on prefix length
	// /31: peer is (odd ↔ even) - RFC 3021 point-to-point
	// /30: peer is +1 from switch IP (assumes switch uses .1, host gets .2)
	// /24+: use offset pattern (.10, .20, .30...)
	var hostOctet int
	if prefixLen == 31 {
		// For /31, toggle between even and odd (e.g., .0↔.1, .2↔.3)
		if lastOctet%2 == 0 {
			hostOctet = lastOctet + 1
		} else {
			hostOctet = lastOctet - 1
		}
	} else if prefixLen == 30 {
		// For /30, assume switch uses .1, host gets .2
		hostOctet = lastOctet + 1
	} else {
		// /24 and larger: offset pattern
		hostOctet = (hostIndex + 1) * 10
	}

	return fmt.Sprintf("%s.%s.%s.%d%s", parts[0], parts[1], parts[2], hostOctet, mask)
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
				DeviceType:  node.DeviceType,
				Image:       node.Image,
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
			Phase:       "booting",
			DeviceType:  node.DeviceType,
			Image:       node.Image,
			SSHPort:     node.SSHPort,
			ConsolePort: node.ConsolePort,
			Host:        node.Host,
			HostIP:      remoteIP,
		}
		SaveState(l.State)
		l.progress("start", fmt.Sprintf("booted %s (pid %d)", name, pid))
	}
	return firstErr
}

// setNodePhase sets the Phase field on all running nodes.
func (l *Lab) setNodePhase(phase string) {
	for _, ns := range l.State.Nodes {
		if ns.Status == "running" {
			ns.Phase = phase
		}
	}
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
// pubKey is the authorized_keys entry to inject for passwordless access; empty = skip injection.
func (l *Lab) bootstrapNodes(ctx context.Context, pubKey string) error {
	// Phase 1: Bootstrap network via serial console (parallel).
	// QEMU user-mode networking requires eth0 up + DHCP before SSH port forwarding works.
	// Host devices use a simpler bootstrap that only waits for the login prompt.
	// Switch nodes also receive the lab SSH public key via console injection.
	err := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		consoleHost := resolveHostIP(node.Host, l.Config)
		if node.DeviceType == "host" || node.DeviceType == "host-vm" {
			return BootstrapHostNetwork(ctx, consoleHost, node.ConsolePort,
				node.ConsoleUser, node.ConsolePass,
				time.Duration(node.BootTimeout)*time.Second)
		}
		return BootstrapNetwork(ctx, consoleHost, node.ConsolePort,
			node.ConsoleUser, node.ConsolePass, node.SSHUser, node.SSHPass,
			time.Duration(node.BootTimeout)*time.Second)
	})

	// Phase 2: Wait for SSH readiness (parallel) — only for nodes still running.
	if err2 := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		sshHost := resolveHostIP(node.Host, l.Config)
		return WaitForSSH(ctx, sshHost, node.SSHPort, node.SSHUser, node.SSHPass,
			60*time.Second)
	}); err == nil {
		err = err2
	}

	// Phase 3: Inject SSH key into all nodes via SSH (sequential, best-effort).
	// Console-based injection is unreliable; injecting after SSH is ready is simpler.
	if pubKey != "" {
		for name, node := range l.Nodes {
			ns := l.State.Nodes[name]
			if ns == nil || ns.Status != "running" {
				continue
			}
			sshHost := resolveHostIP(node.Host, l.Config)
			if injErr := injectSSHKeyViaSSH(sshHost, ns.SSHPort, node.SSHUser, node.SSHPass, pubKey); injErr != nil {
				util.Logger.Warnf("newtlab: inject SSH key into %s: %v", name, injErr)
			}
		}
	}

	return err
}

// applyNodePatches resolves and applies platform boot patches per node (parallel),
// then patches device profiles.
func (l *Lab) applyNodePatches(ctx context.Context) error {
	err := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		if node.DeviceType == "host" || node.DeviceType == "host-vm" {
			return nil // host devices have no platform boot patches
		}
		// Use resolved platform from NodeConfig (may come from profile or topology)
		platform := l.Platform.Platforms[node.Platform]
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

	// Kill QEMU processes (skip virtual host entries — killed with parent VM)
	for name, node := range state.Nodes {
		if node.VMName != "" {
			continue // virtual host — killed with parent VM
		}
		if IsRunning(node.PID, node.HostIP) {
			l.progress("stop", fmt.Sprintf("stopping %s", name))
			if err := StopNode(node.PID, node.HostIP); err != nil {
				errs = append(errs, fmt.Errorf("stop %s (pid %d): %w", name, node.PID, err))
			}
		}
	}

	// Stop bridge processes (per-host)
	l.progress("bridges", "stopping bridge workers")
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

	// If the old process is still running (e.g., SSH timed out on previous Start),
	// skip QEMU launch and just re-try SSH connectivity.
	if nodeState.PID > 0 && IsRunning(nodeState.PID, nodeState.HostIP) {
		nodeState.Status = "running"
	} else {
		pid, err := StartNode(node, LabDir(l.Name), nodeState.HostIP)
		if err != nil {
			return err
		}
		nodeState.PID = pid
		nodeState.Status = "running"
	}

	// Wait for SSH — resolve host IP for the node
	sshHost := resolveHostIP(node.Host, lab.Config)
	if err := WaitForSSH(ctx, sshHost, node.SSHPort, node.SSHUser, node.SSHPass,
		time.Duration(node.BootTimeout)*time.Second); err != nil {
		nodeState.Status = "error"
		if saveErr := SaveState(state); saveErr != nil {
			return fmt.Errorf("save state after SSH failure: %w (original: %v)", saveErr, err)
		}
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
		// Skip host/host-vm devices — they have no SONiC CONFIG_DB to provision
		if ns := state.Nodes[name]; ns != nil && (ns.DeviceType == "host" || ns.DeviceType == "host-vm") {
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cmd := exec.CommandContext(ctx, findSiblingBinary("newtron"), "provision", "-S", l.SpecDir, "-d", name, "-x")
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
		// Skip host/host-vm devices — they have no FRR/BGP
		if nc.DeviceType == "host" || nc.DeviceType == "host-vm" {
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

	// Kill QEMU processes (skip virtual host entries)
	for name, node := range existing.Nodes {
		if node.VMName != "" {
			continue
		}
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
