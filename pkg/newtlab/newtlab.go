package newtlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// NewtronClient is the interface newtlab needs from newtron's HTTP client —
// both the spec reads it consumes and the reconcile operation it invokes to
// provision a device. Defined here (rather than imported) so test code can mock
// without depending on the full pkg/newtron/client package, and so the boundary
// between engines is explicit (DESIGN_PRINCIPLES_NEWTRON.md §27 — newtron owns
// spec data and device state; newtlab reaches both through newtron's HTTP API,
// never by spawning its binary). *pkg/newtron/client.Client satisfies this
// interface structurally.
type NewtronClient interface {
	GetTopology() (*spec.TopologySpecFile, error)
	ListPlatforms() (map[string]*spec.PlatformSpec, error)
	ShowNodeSpec(name string) (*spec.NodeSpec, error)
	// NetworkID is the network this client is bound to.
	NetworkID() string
	// Reconcile delivers a device's projection to the device — the provision
	// operation. Same method newtrun's provision step calls; the single owner of
	// "reconcile a device" is newtron's HTTP client, and both engines route
	// through it (DESIGN_PRINCIPLES_NEWTRON.md §27, ai-instructions §25).
	Reconcile(device, mode, reconcileMode string, opts newtron.ExecOpts) (*newtron.ReconcileResult, error)
	// RefreshBGP forces a BGP soft clear on the device to re-advertise routes —
	// the post-provision convergence nudge. Device interaction (vtysh) is
	// newtron's alone (§27), so newtlab asks newtron rather than SSHing in.
	RefreshBGP(device string) error
}

// HostVMGroup represents virtual hosts coalesced into one VM.
type HostVMGroup struct {
	VMName  string                     // synthetic VM name (e.g., "hostvm-0")
	Hosts   []string                   // sorted logical host names
	NICBase map[string]int             // host name → base NIC index on VM
	Ports   map[string][]spec.PortSpec // host name → its platform port inventory
}

// Lab is the top-level newtlab orchestrator. It consumes newtron specs
// via the HTTP API (§27 — newtron owns spec files), resolves VM
// configuration, and manages QEMU processes.
type Lab struct {
	// NetworkID is the lab's identity — the newtron network it realizes (#396).
	// It keys the state directory and the /labs/{networkID} API; there is no
	// separate lab name.
	NetworkID    string
	StateDir     string
	Topology     *spec.TopologySpecFile
	Platform     map[string]*spec.PlatformSpec
	Profiles     map[string]*spec.NodeSpec
	Config       *VMLabConfig
	Nodes        map[string]*NodeConfig
	Links        []*LinkConfig
	State        *LabState
	HostVMs      []*HostVMGroup
	Force        bool
	DeviceFilter []string // if non-empty, only provision these devices

	// OrchestratorURL is the base URL of newtlab-server (or the
	// composed newt-server). newtlink processes started by setupBridges
	// push their BridgeStats here every pushInterval — see #118 and
	// pkg/newtlab/bridge.go. Set by the caller before Deploy (CLI:
	// resolved from --newtlab-server flag / NEWTLAB_SERVER env var;
	// HTTP path: set by handleDeploy from server config). Empty causes
	// setupBridges to fail rather than spawn workers that push to
	// nowhere — the bridge-stats push is mandatory once newtlink no
	// longer offers a fallback listener.
	OrchestratorURL string

	// newtronClient is the newtron HTTP client used to read spec data and to
	// invoke reconcile (provision) — every newtlab→newtron call routes through
	// it (§27). Set by NewLab; not mutable after construction.
	newtronClient NewtronClient

	// OnProgress is an optional callback for progress on the long-running lab
	// operations (deploy, provision). Called from the operation's goroutines —
	// including parallel per-device provision — so an implementation must be
	// safe for concurrent calls. The API server wires it to the per-lab SSE
	// broker; the CLI wires it to stdout.
	OnProgress func(phase, detail string)
}

func (l *Lab) progress(phase, detail string) {
	if l.OnProgress != nil {
		l.OnProgress(phase, detail)
	}
}

// NewLab loads specs from newtron-server via HTTP and returns a
// configured Lab. The client must be configured for the network that
// owns the named topology.
//
// networkID is the lab's identity — the newtron network it realizes (#396).
// It appears as the state-directory name under ~/.newtlab/labs/<networkID>/
// and as the /labs/{networkID} key. The client must have been constructed for
// that same network (per-network HTTP client per pkg/newtron/client conventions);
// client.NetworkID() and this argument are the same network.
//
// newtlab no longer reads spec JSON files directly (DESIGN_PRINCIPLES
// §27 — newtron owns the spec files and exposes their contents through
// `/newtron/v1/network/...`).
func NewLab(ctx context.Context, client NewtronClient, networkID string) (*Lab, error) {
	if client == nil {
		return nil, fmt.Errorf("newtlab: nil newtron client")
	}
	if networkID == "" {
		return nil, fmt.Errorf("newtlab: network id required")
	}

	l := &Lab{
		NetworkID:     networkID,
		StateDir:      LabDir(networkID),
		Profiles:      make(map[string]*spec.NodeSpec),
		Nodes:         make(map[string]*NodeConfig),
		newtronClient: client,
	}

	// Topology from newtron
	topo, err := client.GetTopology()
	if err != nil {
		return nil, fmt.Errorf("newtlab: get topology from newtron: %w", err)
	}
	l.Topology = topo

	// Links must be explicit in topology
	if len(l.Topology.Links) == 0 {
		util.Logger.Infof("newtlab: no links defined in topology %q", networkID)
	}

	// Platforms from newtron
	platforms, err := client.ListPlatforms()
	if err != nil {
		return nil, fmt.Errorf("newtlab: get platforms from newtron: %w", err)
	}
	l.Platform = platforms

	// Per-nodes from newtron
	for deviceName := range l.Topology.Nodes {
		profile, err := client.ShowNodeSpec(deviceName)
		if err != nil {
			return nil, fmt.Errorf("newtlab: get profile %s from newtron: %w", deviceName, err)
		}
		l.Profiles[deviceName] = profile
	}

	// Resolve newtlab config with defaults
	l.Config = resolveNewtLabConfig(l.Topology.NewtLab)

	// Resolve node configs
	sortedNames := l.Topology.DeviceNames()
	usedPorts := map[int]bool{} // track allocated ports to avoid double-use
	for i, name := range sortedNames {
		profile := l.Profiles[name]
		var platform *spec.PlatformSpec
		platformName := profile.Platform
		if platformName == "" {
			// Fallback to topology default platform
			platformName = l.Topology.Platform
		}
		if platformName != "" {
			platform = l.Platform[platformName]
		}

		nc, err := ResolveNodeConfig(name, profile, platform)
		if err != nil {
			return nil, err
		}

		// Allocate SSH and console ports, auto-resolving conflicts
		preferredSSH := l.Config.SSHPortBase + i
		sshPort, err := findFreeLocalPort(preferredSSH, usedPorts)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate SSH port for %s: %w", name, err)
		}
		if sshPort != preferredSSH {
			util.Logger.Infof("newtlab: %s SSH port %d in use, using %d", name, preferredSSH, sshPort)
		}
		nc.SSHPort = sshPort
		usedPorts[sshPort] = true

		preferredConsole := l.Config.ConsolePortBase + i
		consolePort, err := findFreeLocalPort(preferredConsole, usedPorts)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate console port for %s: %w", name, err)
		}
		if consolePort != preferredConsole {
			util.Logger.Infof("newtlab: %s console port %d in use, using %d", name, preferredConsole, consolePort)
		}
		nc.ConsolePort = consolePort
		usedPorts[consolePort] = true

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
	l.Links, err = AllocateLinks(l.Topology.Links, l.Nodes, l.Config, hostMap, usedPorts)
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
		profile *spec.NodeSpec
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
			Name:        vmName,
			Platform:    templateNC.Platform,
			DeviceType:  "host-vm",
			Image:       templateNC.Image,
			Memory:      templateNC.Memory,
			CPUs:        templateNC.CPUs,
			NICDriver:   templateNC.NICDriver,
			Ports:       templateNC.Ports,
			CPUFeatures: templateNC.CPUFeatures,
			SSHUser:     templateNC.SSHUser,
			SSHPass:     templateNC.SSHPass,
			ConsoleUser: templateNC.ConsoleUser,
			ConsolePass: templateNC.ConsolePass,
			BootTimeout: templateNC.BootTimeout,
			Host:        templateNC.Host,
			SSHPort:     templateNC.SSHPort,
			ConsolePort: templateNC.ConsolePort,
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
			Ports:   make(map[string][]spec.PortSpec),
		}

		// Assign each host a contiguous NIC range in the shared VM. A host
		// interface resolves to NICBase + (nic_index-1), so the range must span
		// the host's highest-used interface ordinal. Sizing it from the ports[]
		// authority (not a link count) keeps the layout deterministic even when a
		// host wires non-contiguous interfaces — e.g. eth0 and eth3 need four
		// slots, not two. NIC 0 is mgmt, so data ranges start at 1.
		nicIdx := 1
		for _, h := range groupHosts {
			group.Hosts = append(group.Hosts, h.name)
			group.NICBase[h.name] = nicIdx
			ports := l.Nodes[h.name].Ports
			group.Ports[h.name] = ports

			maxOrdinal := 0
			for _, link := range l.Topology.Links {
				for _, ep := range []string{link.A, link.Z} {
					dev, iface, _ := splitLinkEndpoint(ep)
					if dev != h.name {
						continue
					}
					// Resolve against the same inventory AllocateLinks uses;
					// unresolvable names are AllocateLinks' error to report, not
					// this sizing pass's — skip them here.
					if ord, err := ResolveNICIndex(ports, iface); err == nil && ord > maxOrdinal {
						maxOrdinal = ord
					}
				}
			}
			nicIdx += maxOrdinal
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
				Ports:   group.Ports[hostName],
			}
		}
	}
	return hostMap
}

// Deploy creates overlay disks, starts QEMU processes, waits for SSH,
// and patches profiles. The context is checked at each major phase and
// threaded into long-running sub-operations.
func (l *Lab) Deploy(ctx context.Context) error {
	util.Logger.Infof("newtlab: deploying lab %s", l.NetworkID)

	// Check for stale state
	if existing, err := LoadState(l.NetworkID); err == nil && existing != nil {
		if !l.Force {
			return fmt.Errorf("newtlab: lab %s already deployed (created %s); use --force to redeploy",
				l.NetworkID, existing.Created.Format(time.RFC3339))
		}
		if err := l.destroyExisting(existing); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cleanup of existing lab failed: %v\n", err)
		}
	}

	// Port conflict detection (SSH, console, link, bridge stats — local and remote).
	// Excluding the lab's own name lets attribution skip stale self-records when
	// a redeploy collides with an in-flight teardown.
	allocs := CollectAllPorts(l)
	if err := ProbeAllPorts(allocs, l.NetworkID); err != nil {
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
		NetworkID:  l.NetworkID,
		Created:    time.Now(),
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
				if err := setupRemoteStateDir(l.NetworkID, hostIP); err != nil {
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
			remoteOverlay := fmt.Sprintf("~/.newtlab/labs/%s/disks/%s.qcow2", l.NetworkID, name)
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

			// Write a per-namespace profile so interactive shells show the
			// logical host name in the prompt (e.g. "host1:/root# ").
			// ip netns exec bind-mounts /etc/netns/<name>/* onto /etc/*,
			// so "profile" maps to /etc/profile which Alpine sources.
			cmds = append(cmds,
				fmt.Sprintf("mkdir -p /etc/netns/%s", hostName),
				fmt.Sprintf("echo 'export PS1=\"%s:\\w# \"' > /etc/netns/%s/profile", hostName, hostName),
			)

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
// Scans the links array to find which switch interface connects to this host,
// then searches the switch device's steps for an IP address on that interface.
// Returns the peer IP (without mask) and the mask (e.g., "/24"), or empty strings if not found.
func (l *Lab) findPeerInterfaceIP(hostName string) (string, string) {
	// Step 1: Find the link connecting to this host. splitLinkEndpoint is the
	// sole owner of "device:interface" parsing (§27) — use the device AND
	// interface it already returns rather than re-splitting the raw endpoint.
	var switchName, switchIntf string
	for _, link := range l.Topology.Links {
		aDevice, aIntf, errA := splitLinkEndpoint(link.A)
		zDevice, zIntf, errZ := splitLinkEndpoint(link.Z)
		if errA != nil || errZ != nil {
			continue
		}
		if aDevice == hostName {
			// Z side is the switch
			switchName, switchIntf = zDevice, zIntf
			break
		}
		if zDevice == hostName {
			// A side is the switch
			switchName, switchIntf = aDevice, aIntf
			break
		}
	}
	if switchName == "" || switchIntf == "" {
		return "", ""
	}

	// Step 2: Find the IP in the switch device's steps
	device, ok := l.Topology.Nodes[switchName]
	if !ok {
		return "", ""
	}
	for _, step := range device.Steps {
		// Match interface-scoped steps for this interface
		if !strings.Contains(step.URL, "/interfaces/"+switchIntf+"/") {
			continue
		}
		// Look for ip_address (apply-service) or ip (configure-interface)
		if ip, ok := step.Params["ip_address"]; ok {
			return splitIPMask(fmt.Sprintf("%v", ip))
		}
		if ip, ok := step.Params["ip"]; ok {
			return splitIPMask(fmt.Sprintf("%v", ip))
		}
	}
	return "", ""
}

// splitIPMask parses "10.1.100.1/24" → ("10.1.100.1", "/24").
func splitIPMask(cidr string) (string, string) {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) == 2 {
		return parts[0], "/" + parts[1]
	}
	return cidr, ""
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

// ResyncBridges re-establishes link telemetry for an already-running lab
// without touching its VMs or its data plane. It ensures the lab has a
// TelemetryToken (minting one if a pre-token deploy left it empty), injects that
// token into the local worker's on-disk bridge.json, and sends the running
// newtlink a SIGHUP so it hot-reloads the credential. newtlink is NOT restarted:
// it relays the QEMU socket connections between VMs, and those netdevs don't
// reconnect — restarting it would drop the data plane. SIGHUP keeps the bridge
// workers (and their connections) alive and only rotates the push token. This
// is the "resync to a running lab, including newtlink" operation, distinct from
// a destroy+redeploy.
//
// A lab with no bridge workers (no links) is a no-op. Remote (multi-host)
// workers are not resynced in place yet — their bridge.json and newtlink live
// on another host — so ResyncBridges refuses, naming the host, and the operator
// redeploys instead. Returns the updated state.
func ResyncBridges(name string) (*LabState, error) {
	state, err := LoadState(name)
	if err != nil {
		return nil, err
	}
	if len(state.Bridges) == 0 {
		return state, nil // no links / no bridge workers — nothing to resync
	}
	for host, bs := range state.Bridges {
		if host != "" || bs.HostIP != "" {
			return nil, fmt.Errorf("newtlab: lab %q has a remote bridge worker on %q; in-place resync of remote workers is not supported — redeploy the lab", name, host)
		}
	}
	if state.TelemetryToken == "" {
		tok, err := NewTelemetryToken()
		if err != nil {
			return nil, err
		}
		state.TelemetryToken = tok
	}

	// Inject the token into the on-disk bridge.json, then SIGHUP the running
	// newtlink to hot-reload it. The bridge workers — and the VM connections
	// they relay — keep running.
	stateDir := LabDir(name)
	if err := injectBridgeToken(filepath.Join(stateDir, "bridge.json"), state.TelemetryToken); err != nil {
		return nil, err
	}
	bs := state.Bridges[""]
	if bs == nil || bs.PID == 0 {
		return nil, fmt.Errorf("newtlab: lab %q has no local bridge worker to signal", name)
	}
	if err := syscall.Kill(bs.PID, syscall.SIGHUP); err != nil {
		return nil, fmt.Errorf("newtlab: signal newtlink (pid %d) to reload token: %w", bs.PID, err)
	}
	if err := SaveState(state); err != nil {
		return nil, err
	}
	return state, nil
}

// setupBridges starts bridge worker processes for inter-VM networking.
// Groups links by host and starts a local or remote bridge per host.
func (l *Lab) setupBridges(ctx context.Context) error {
	if len(l.Links) == 0 {
		return nil
	}

	if l.OrchestratorURL == "" {
		return fmt.Errorf("newtlab: OrchestratorURL not set; newtlink pushes BridgeStats to newtlab-server and has no fallback listener (#118)")
	}

	hostLinks := map[string][]*LinkConfig{}
	for _, lc := range l.Links {
		hostLinks[lc.WorkerHost] = append(hostLinks[lc.WorkerHost], lc)
	}

	// Mint the per-lab telemetry token once, so every worker's bridge.json
	// carries the same credential. It lands in l.State and is persisted by the
	// deploy's SaveState, so a server restart re-reads it and the running
	// newtlink keeps authenticating without a redeploy.
	token, err := NewTelemetryToken()
	if err != nil {
		return err
	}
	l.State.TelemetryToken = token

	l.State.Bridges = make(map[string]*BridgeState)

	for _, host := range sortedHosts(hostLinks) {
		links := hostLinks[host]
		hostIP := resolveHostIP(host, l.Config)
		push := BridgePushParams{
			OrchestratorURL: l.OrchestratorURL,
			LabName:         l.NetworkID,
			WorkerHost:      host,
			Token:           token,
		}

		if host == "" {
			if err := WriteBridgeConfig(l.StateDir, links, push); err != nil {
				return err
			}
			pid, err := startBridgeProcess(l.NetworkID, l.StateDir)
			if err != nil {
				return fmt.Errorf("newtlab: start bridge: %w", err)
			}
			l.State.Bridges[""] = &BridgeState{PID: pid}
		} else {
			cfg := buildBridgeConfig(links, push)
			configJSON, err := json.MarshalIndent(cfg, "", "    ")
			if err != nil {
				return fmt.Errorf("newtlab: marshal remote bridge config: %w", err)
			}
			pid, err := startBridgeProcessRemote(l.NetworkID, hostIP, configJSON)
			if err != nil {
				return fmt.Errorf("newtlab: start remote bridge on %s: %w", hostIP, err)
			}
			l.State.Bridges[host] = &BridgeState{PID: pid, HostIP: hostIP}
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
			SSHUser:     node.SSHUser,
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
		// Pre-bootstrapped images (e.g. Junos baked with a config commit)
		// skip the console-driven Linux-style bring-up entirely. Phase 2
		// (WaitForSSH) below still runs to confirm reachability.
		if node.SkipBootstrap {
			return nil
		}
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
	// Pre-bootstrapped images skip Phase 1, so the VM is still booting from
	// cold when we get here; use the full BootTimeout. For Phase-1 nodes
	// (network just came up via console), 60s is plenty.
	if err2 := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		sshHost := resolveHostIP(node.Host, l.Config)
		sshTimeout := 60 * time.Second
		if node.SkipBootstrap {
			sshTimeout = time.Duration(node.BootTimeout) * time.Second
		}
		return WaitForSSH(ctx, sshHost, node.SSHPort, node.SSHUser, node.SSHPass,
			sshTimeout)
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
// then patches nodes.
func (l *Lab) applyNodePatches(ctx context.Context) error {
	err := l.parallelForNodes(func(name string, node *NodeConfig, ns *NodeState) error {
		if node.DeviceType == "host" || node.DeviceType == "host-vm" {
			return nil // host devices have no platform boot patches
		}
		// Use resolved platform from NodeConfig (may come from profile or topology)
		platform := l.Platform[node.Platform]
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

	return err
}

// StopByName stops a node in a lab identified by its network-id and node name,
// loading all necessary state from disk.
func StopByName(ctx context.Context, networkID, nodeName string) error {
	lab := &Lab{NetworkID: networkID}
	return lab.Stop(ctx, nodeName)
}

// StartByName starts a node in a lab identified by its network-id and node name,
// loading all necessary state from disk.
func StartByName(ctx context.Context, networkID, nodeName string) error {
	lab := &Lab{NetworkID: networkID}
	return lab.Start(ctx, nodeName)
}

// Destroy kills QEMU processes, removes overlays, cleans state,
// and restores profiles. The context allows cancellation of the
// teardown sequence.
func (l *Lab) Destroy(ctx context.Context) error {
	util.Logger.Infof("newtlab: destroying lab %s", l.NetworkID)

	state, err := LoadState(l.NetworkID)
	if err != nil {
		return err
	}
	l.State = state

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
	errs = append(errs, cleanupAllRemoteHosts(l.NetworkID, state)...)

	// Remove local state directory
	if err := RemoveState(l.NetworkID); err != nil {
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
	state, err := LoadState(l.NetworkID)
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
	state, err := LoadState(l.NetworkID)
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
	state, err := LoadState(l.NetworkID)
	if err != nil {
		return err
	}

	nodeState, ok := state.Nodes[nodeName]
	if !ok {
		return fmt.Errorf("newtlab: node %q not found", nodeName)
	}

	// Re-load lab config to rebuild the node. The original Lab's
	// newtronClient is reused — newtlab is not the owner of spec data,
	// so re-fetching from newtron picks up any operator edits since
	// the original Deploy() was called.
	if l.newtronClient == nil {
		return fmt.Errorf("newtlab: cannot reload — no newtron client (lab was constructed without one)")
	}
	lab, err := NewLab(ctx, l.newtronClient, l.NetworkID)
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
		pid, err := StartNode(node, LabDir(l.NetworkID), nodeState.HostIP)
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

// Provision reconciles every (or the filtered set of) devices to their topology
// projection, in parallel up to `parallel`, by calling newtron's HTTP client
// Reconcile (the single owner of that operation, §27) — not by spawning the
// newtron binary. ctx bounds the overall call; per-device cancellation follows
// the client's own request handling.
func (l *Lab) Provision(ctx context.Context, parallel int) error {
	util.Logger.Infof("newtlab: provisioning lab %s (parallel=%d)", l.NetworkID, parallel)
	state, err := LoadState(l.NetworkID)
	if err != nil {
		return err
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

	// Switches only — host/host-vm devices have no SONiC CONFIG_DB to provision.
	// Collected up front so per-device progress can report "k/total".
	var switches []string
	for name := range state.Nodes {
		if ns := state.Nodes[name]; ns != nil && (ns.DeviceType == "host" || ns.DeviceType == "host-vm") {
			continue
		}
		switches = append(switches, name)
	}
	total := len(switches)
	l.progress("provision", fmt.Sprintf("reconciling %d device(s)", total))

	sem := make(chan struct{}, parallel)
	var mu sync.Mutex
	var errs []error
	var done int64 // atomic — completed device count for progress

	var wg sync.WaitGroup
	for _, name := range switches {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Provision = reconcile the device's topology projection. Route
			// through newtron's HTTP client — the single owner of "reconcile a
			// device" (§27), the same method newtrun's provision step calls —
			// rather than spawning the newtron binary. The client carries the
			// caller's identity, so this needs no session-cache lookup.
			if _, err := l.newtronClient.Reconcile(name, "topology", "", newtron.ExecOpts{Execute: true}); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("provision %s: %w", name, err))
				mu.Unlock()
				l.progress("provision", fmt.Sprintf("failed %s", name))
				return
			}
			l.progress("provision", fmt.Sprintf("reconciled %s (%d/%d)", name, atomic.AddInt64(&done, 1), total))
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
	l.progress("bgp-refresh", "refreshing BGP to converge routes")
	l.refreshBGP(state)

	return nil
}

// refreshBGP asks newtron to run a BGP soft clear on each provisioned switch,
// forcing route re-advertisement once all devices are up. Errors are logged but
// not returned — this is a best-effort convergence aid.
//
// The soft clear is a device operation (vtysh), which is newtron's alone (§27);
// newtlab does not SSH to devices. It routes through the same newtron client it
// provisions with — newtron resolves the device's SSH port (via newtlab's own
// port resolver) and carries the caller's identity, so this needs no per-device
// credentials or dial here.
//
// bgpRefreshDelay is the wait after provisioning before the clear, to let the
// last device's BGP session initialize. A var (not const) so tests can zero it.
var bgpRefreshDelay = 5 * time.Second

func (l *Lab) refreshBGP(state *LabState) {
	// Brief delay for the last-provisioned device's BGP to start.
	time.Sleep(bgpRefreshDelay)

	for name := range state.Nodes {
		nc := l.Nodes[name]
		if nc == nil {
			continue
		}
		// Skip host/host-vm devices — they have no FRR/BGP.
		if nc.DeviceType == "host" || nc.DeviceType == "host-vm" {
			continue
		}
		if err := l.newtronClient.RefreshBGP(name); err != nil {
			util.Logger.Warnf("refreshBGP %s: %v", name, err)
		} else {
			util.Logger.Infof("refreshBGP %s: done", name)
		}
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
	errs = append(errs, cleanupAllRemoteHosts(l.NetworkID, existing)...)

	// Remove local state
	if err := RemoveState(l.NetworkID); err != nil {
		errs = append(errs, fmt.Errorf("remove state: %w", err))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// resolveNewtLabConfig returns a VMLabConfig with defaults applied.
// The runtime Hosts map (name → address) is derived from cfg.Servers;
// there is no longer a parallel legacy Hosts input field (deleted per
// §40 Greenfield). Single-host labs leave cfg.Servers nil and the
// runtime Hosts map stays nil — resolveHostIP falls back to the host
// name itself in that case.
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
