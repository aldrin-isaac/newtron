package newtron

import (
	"fmt"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// InterfaceInventoryEntry is one interface a node's platform supports, as
// returned by GET /nodes/{node}/interfaces. It joins three spec-level facts:
// the platform port (device-native name + NIC slot), whether the interface is
// wired in the topology (Used + Peer), and its authored port config (Config,
// nil when unconfigured). A client enumerates every supported interface and
// selects the connectable ones — those with Used == false.
type InterfaceInventoryEntry struct {
	Name     string           `json:"name"`
	NICIndex int              `json:"nic_index"`
	Used     bool             `json:"used"`             // wired by a topology link
	Peer     string           `json:"peer,omitempty"`   // "device:interface" the link connects to
	Config   *spec.PortConfig `json:"config,omitempty"` // authored port config; nil ⇒ unconfigured
}

// NodeInterfaceInventory returns every interface the named device's platform
// supports, annotated with topology wiring (Used/Peer) and authored port config
// (Config). It is a spec-level read — the platform port inventory joined with
// the topology — so it answers uniformly for switches and hosts (which have no
// SONiC device) and works offline, before anything is deployed. This is the
// single authority a client uses to enumerate a node's interfaces and pick the
// connectable ones; newtlab and newtrun validate interface references against
// the same platform inventory. Live per-interface state (admin/oper status,
// addresses) remains at GET /nodes/{node}/interfaces/{name}.
func (net *Network) NodeInterfaceInventory(device string) ([]InterfaceInventoryEntry, error) {
	nodeSpec, err := net.ShowNodeSpec(device)
	if err != nil {
		return nil, err
	}
	if nodeSpec.Platform == "" {
		return nil, fmt.Errorf("device %q has no platform — cannot determine supported interfaces", device)
	}
	platform, err := net.ShowPlatform(nodeSpec.Platform)
	if err != nil {
		return nil, err
	}

	// Topology contributions are optional — a node may be defined before it is
	// placed in the topology or wired. Index this device's link usage as
	// interface name → the peer endpoint on the far side.
	var topoNode *spec.TopologyNode
	peerByIface := map[string]string{}
	if topo := net.GetTopology(); topo != nil {
		topoNode = topo.Nodes[device]
		for _, l := range topo.Links {
			if iface, peer, ok := linkEndpointFor(device, l); ok {
				peerByIface[iface] = peer
			}
		}
	}

	out := make([]InterfaceInventoryEntry, 0, len(platform.Ports))
	for _, p := range platform.Ports {
		e := InterfaceInventoryEntry{Name: p.Name, NICIndex: p.NICIndex}
		if peer, ok := peerByIface[p.Name]; ok {
			e.Used = true
			e.Peer = peer
		}
		if topoNode != nil {
			if cfg, ok := topoNode.Ports[p.Name]; ok {
				e.Config = cfg
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// linkEndpointFor reports this device's interface on a link and the far-side
// endpoint ("device:interface"), if the device is one of the link's ends.
func linkEndpointFor(device string, l *spec.TopologyLink) (iface, peer string, ok bool) {
	if d, i, split := splitDeviceIface(l.A); split && d == device {
		return i, l.Z, true
	}
	if d, i, split := splitDeviceIface(l.Z); split && d == device {
		return i, l.A, true
	}
	return "", "", false
}

// splitDeviceIface splits a "device:interface" endpoint into its parts.
func splitDeviceIface(endpoint string) (device, iface string, ok bool) {
	parts := strings.SplitN(endpoint, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
