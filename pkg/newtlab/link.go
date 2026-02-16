package newtlab

import (
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// countingWriter wraps an io.Writer and counts bytes written.
type countingWriter struct {
	w     io.Writer
	count *atomic.Int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.count.Add(int64(n))
	return n, err
}

// LinkConfig represents a resolved link between two device NICs.
type LinkConfig struct {
	A          LinkEndpoint
	Z          LinkEndpoint
	APort      int    // TCP port for bridge worker A-side listener
	ZPort      int    // TCP port for bridge worker Z-side listener
	ABind      string // bind address for A listener ("127.0.0.1" or "0.0.0.0")
	ZBind      string // bind address for Z listener
	WorkerHost string // host that runs the bridge worker (empty = local)
}

// LinkEndpoint identifies one side of a link.
type LinkEndpoint struct {
	Device    string // device name
	Interface string // SONiC interface name (e.g. "Ethernet0")
	NICIndex  int    // QEMU NIC index (after interface map resolution)
}

// VMLabConfig mirrors spec.NewtLabConfig with resolved defaults.
type VMLabConfig struct {
	LinkPortBase    int               // default: 20000
	ConsolePortBase int               // default: 30000
	SSHPortBase     int               // default: 40000
	Hosts           map[string]string // host name → IP
	Servers         []*spec.ServerConfig // server pool (nil = single-host mode)
}

// HostMapping maps a coalesced host device to its parent VM and NIC base.
type HostMapping struct {
	VMName  string
	NICBase int
}

// AllocateLinks resolves topology links into LinkConfig entries with
// port allocations, NIC index assignments, and bridge worker placement.
// hostMap maps coalesced host device names to their parent VM and NIC base;
// pass nil when no host coalescing is used.
func AllocateLinks(
	links []*spec.TopologyLink,
	nodes map[string]*NodeConfig,
	config *VMLabConfig,
	hostMap map[string]HostMapping,
) ([]*LinkConfig, error) {
	var result []*LinkConfig

	for i, link := range links {
		aPort := config.LinkPortBase + (i * 2)
		zPort := config.LinkPortBase + (i * 2) + 1

		aDevice, aIface, err := splitLinkEndpoint(link.A)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate links: link %d A: %w", i, err)
		}
		zDevice, zIface, err := splitLinkEndpoint(link.Z)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate links: link %d Z: %w", i, err)
		}

		// Resolve coalesced hosts: remap device name to VM, compute NIC index
		var aNIC, zNIC int
		aResolved, zResolved := false, false

		if mapping, ok := hostMap[aDevice]; ok {
			ethIdx := parseLinuxEthIndex(aIface)
			if ethIdx < 0 {
				return nil, fmt.Errorf("newtlab: allocate links: link %d A: invalid host interface %q", i, aIface)
			}
			aNIC = mapping.NICBase + ethIdx
			aDevice = mapping.VMName
			aResolved = true
		}
		if mapping, ok := hostMap[zDevice]; ok {
			ethIdx := parseLinuxEthIndex(zIface)
			if ethIdx < 0 {
				return nil, fmt.Errorf("newtlab: allocate links: link %d Z: invalid host interface %q", i, zIface)
			}
			zNIC = mapping.NICBase + ethIdx
			zDevice = mapping.VMName
			zResolved = true
		}

		nodeA, ok := nodes[aDevice]
		if !ok {
			return nil, fmt.Errorf("newtlab: allocate links: device %q not found", aDevice)
		}
		nodeZ, ok := nodes[zDevice]
		if !ok {
			return nil, fmt.Errorf("newtlab: allocate links: device %q not found", zDevice)
		}

		if !aResolved {
			aNIC, err = ResolveNICIndex(nodeA.InterfaceMap, aIface, nil)
			if err != nil {
				return nil, fmt.Errorf("newtlab: allocate links: link %d A: %w", i, err)
			}
		}
		if !zResolved {
			zNIC, err = ResolveNICIndex(nodeZ.InterfaceMap, zIface, nil)
			if err != nil {
				return nil, fmt.Errorf("newtlab: allocate links: link %d Z: %w", i, err)
			}
		}

		lc := &LinkConfig{
			A: LinkEndpoint{
				Device:    aDevice,
				Interface: aIface,
				NICIndex:  aNIC,
			},
			Z: LinkEndpoint{
				Device:    zDevice,
				Interface: zIface,
				NICIndex:  zNIC,
			},
			APort: aPort,
			ZPort: zPort,
		}
		result = append(result, lc)
	}

	// Assign worker hosts and compute bind/connect addresses.
	PlaceWorkers(result, nodes)

	for _, lc := range result {
		nodeA := nodes[lc.A.Device]
		nodeZ := nodes[lc.Z.Device]

		// Bind: if the endpoint's VM is on the same host as the worker,
		// bind to 127.0.0.1; otherwise bind to 0.0.0.0 so the remote VM can reach it.
		if nodeA.Host == lc.WorkerHost {
			lc.ABind = "127.0.0.1"
		} else {
			lc.ABind = "0.0.0.0"
		}
		if nodeZ.Host == lc.WorkerHost {
			lc.ZBind = "127.0.0.1"
		} else {
			lc.ZBind = "0.0.0.0"
		}

		// ConnectAddr: VM on worker host → 127.0.0.1:<port>
		// VM on remote host → <worker-host-IP>:<port>
		aConnect := connectAddr(nodeA.Host, lc.WorkerHost, lc.APort, config)
		zConnect := connectAddr(nodeZ.Host, lc.WorkerHost, lc.ZPort, config)

		nodeA.NICs = append(nodeA.NICs, NICConfig{
			Index:       lc.A.NICIndex,
			NetdevID:    fmt.Sprintf("eth%d", lc.A.NICIndex),
			Interface:   lc.A.Interface,
			ConnectAddr: aConnect,
		})
		nodeZ.NICs = append(nodeZ.NICs, NICConfig{
			Index:       lc.Z.NICIndex,
			NetdevID:    fmt.Sprintf("eth%d", lc.Z.NICIndex),
			Interface:   lc.Z.Interface,
			ConnectAddr: zConnect,
		})
	}

	return result, nil
}

// connectAddr returns the "IP:PORT" a VM should connect to for its bridge worker.
// If the VM is on the same host as the worker, it connects to 127.0.0.1.
// Otherwise it needs the worker host's IP.
func connectAddr(vmHost, workerHost string, port int, config *VMLabConfig) string {
	if vmHost == workerHost {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	ip := resolveHostIP(workerHost, config)
	// If worker is local (empty string), resolveHostIP returns "127.0.0.1"
	// but cross-host traffic needs the externally-reachable IP.
	if workerHost == "" {
		if config != nil && config.Hosts != nil {
			if resolved, ok := config.Hosts["local"]; ok {
				ip = resolved
			}
		}
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

// PlaceWorkers assigns a WorkerHost for each link's bridge worker.
// For same-host links, the worker runs on that host.
// For cross-host links, workers are distributed greedily to balance load,
// with alphabetical tie-breaking.
func PlaceWorkers(links []*LinkConfig, nodes map[string]*NodeConfig) {
	hostCount := map[string]int{}
	for _, link := range links {
		hostA := nodes[link.A.Device].Host
		hostZ := nodes[link.Z.Device].Host
		if hostA == hostZ {
			link.WorkerHost = hostA
			continue
		}
		// Greedy: assign to host with fewer cross-host workers, tie-break alphabetically
		if hostCount[hostA] < hostCount[hostZ] || (hostCount[hostA] == hostCount[hostZ] && hostA <= hostZ) {
			link.WorkerHost = hostA
		} else {
			link.WorkerHost = hostZ
		}
		hostCount[link.WorkerHost]++
	}
}

// BridgeWorker manages a single link's TCP bridge between two QEMU VMs.
type BridgeWorker struct {
	Link      *LinkConfig
	aListener net.Listener
	zListener net.Listener
	aToZBytes atomic.Int64
	zToABytes atomic.Int64
	sessions  atomic.Int64
	connected atomic.Bool
}

// Bridge holds all bridge workers and provides lifecycle and stats access.
type Bridge struct {
	workers []*BridgeWorker
	wg      sync.WaitGroup
}

// Stop closes all listeners and waits for goroutines to finish.
func (b *Bridge) Stop() {
	for _, w := range b.workers {
		w.aListener.Close()
		w.zListener.Close()
	}
	b.wg.Wait()
}

// Stats returns a snapshot of all bridge worker counters.
func (b *Bridge) Stats() BridgeStats {
	stats := BridgeStats{
		Links: make([]LinkStats, len(b.workers)),
	}
	for i, w := range b.workers {
		stats.Links[i] = LinkStats{
			A:         w.Link.A.Device + ":" + w.Link.A.Interface,
			Z:         w.Link.Z.Device + ":" + w.Link.Z.Interface,
			APort:     w.Link.APort,
			ZPort:     w.Link.ZPort,
			AToZBytes: w.aToZBytes.Load(),
			ZToABytes: w.zToABytes.Load(),
			Sessions:  w.sessions.Load(),
			Connected: w.connected.Load(),
		}
	}
	return stats
}

// StartBridgeWorkers opens TCP listeners for all links and spawns bridge
// goroutines. Returns a Bridge that provides Stop() and Stats().
func StartBridgeWorkers(links []*LinkConfig) (*Bridge, error) {
	b := &Bridge{
		workers: make([]*BridgeWorker, 0, len(links)),
	}

	// Open all listeners first; on any failure, close everything opened so far.
	cleanup := func() {
		for _, w := range b.workers {
			if w.aListener != nil {
				w.aListener.Close()
			}
			if w.zListener != nil {
				w.zListener.Close()
			}
		}
	}

	for _, link := range links {
		w := &BridgeWorker{
			Link: link,
		}

		aAddr := fmt.Sprintf("%s:%d", link.ABind, link.APort)
		var err error
		w.aListener, err = net.Listen("tcp", aAddr)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("newtlab: bridge listen %s: %w", aAddr, err)
		}

		zAddr := fmt.Sprintf("%s:%d", link.ZBind, link.ZPort)
		w.zListener, err = net.Listen("tcp", zAddr)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("newtlab: bridge listen %s: %w", zAddr, err)
		}

		b.workers = append(b.workers, w)
	}

	// Start bridge goroutines.
	for _, w := range b.workers {
		b.wg.Add(1)
		go func(w *BridgeWorker) {
			defer b.wg.Done()
			w.run()
		}(w)
	}

	return b, nil
}

// run accepts connections on both sides and bridges them. When either
// connection closes, it loops back to re-accept (survives VM restart).
func (w *BridgeWorker) run() {
	for {
		aConn, err := w.aListener.Accept()
		if err != nil {
			return // listener closed — Stop was called
		}
		zConn, err := w.zListener.Accept()
		if err != nil {
			aConn.Close()
			return // listener closed
		}

		w.sessions.Add(1)
		w.connected.Store(true)

		// Bridge: copy in both directions with byte counting
		var copyWg sync.WaitGroup
		copyWg.Add(2)
		go func() {
			defer copyWg.Done()
			io.Copy(&countingWriter{w: aConn, count: &w.zToABytes}, zConn) // Z→A
			aConn.Close()
		}()
		go func() {
			defer copyWg.Done()
			io.Copy(&countingWriter{w: zConn, count: &w.aToZBytes}, aConn) // A→Z
			zConn.Close()
		}()
		copyWg.Wait()
		w.connected.Store(false)
		// Loop back to accept next pair (VM restart)
	}
}

// splitLinkEndpoint splits a "device:interface" string.
func splitLinkEndpoint(endpoint string) (string, string, error) {
	idx := strings.IndexByte(endpoint, ':')
	if idx < 0 {
		return "", "", fmt.Errorf("invalid endpoint format %q (expected device:interface)", endpoint)
	}
	return endpoint[:idx], endpoint[idx+1:], nil
}

// sortedNodeNames returns node names in sorted order.
func sortedNodeNames(nodes map[string]*NodeConfig) []string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
