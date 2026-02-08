package newtlab

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/spec"
)

// LinkConfig represents a resolved link between two device NICs.
type LinkConfig struct {
	A    LinkEndpoint
	Z    LinkEndpoint
	Port int // TCP port for this link
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
}

// AllocateLinks resolves topology links into LinkConfig entries with
// port allocations and NIC index assignments.
func AllocateLinks(
	links []*spec.TopologyLink,
	nodes map[string]*NodeConfig,
	config *VMLabConfig,
) ([]*LinkConfig, error) {
	var result []*LinkConfig

	for i, link := range links {
		port := config.LinkPortBase + i

		aDevice, aIface, err := splitLinkEndpoint(link.A)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate links: link %d A: %w", i, err)
		}
		zDevice, zIface, err := splitLinkEndpoint(link.Z)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate links: link %d Z: %w", i, err)
		}

		nodeA, ok := nodes[aDevice]
		if !ok {
			return nil, fmt.Errorf("newtlab: allocate links: device %q not found", aDevice)
		}
		nodeZ, ok := nodes[zDevice]
		if !ok {
			return nil, fmt.Errorf("newtlab: allocate links: device %q not found", zDevice)
		}

		aNIC, err := ResolveNICIndex(nodeA.InterfaceMap, aIface, nil)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate links: link %d A: %w", i, err)
		}
		zNIC, err := ResolveNICIndex(nodeZ.InterfaceMap, zIface, nil)
		if err != nil {
			return nil, fmt.Errorf("newtlab: allocate links: link %d Z: %w", i, err)
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
			Port: port,
		}
		result = append(result, lc)

		// Determine connect IP for Z side
		connectIP := "127.0.0.1"
		if nodeA.Host != nodeZ.Host && nodeA.Host != "" {
			if ip, ok := config.Hosts[nodeA.Host]; ok {
				connectIP = ip
			}
		}

		// A side listens
		nodeA.NICs = append(nodeA.NICs, NICConfig{
			Index:     aNIC,
			NetdevID:  fmt.Sprintf("eth%d", aNIC),
			Interface: aIface,
			LinkPort:  port,
			Listen:    true,
		})

		// Z side connects
		nodeZ.NICs = append(nodeZ.NICs, NICConfig{
			Index:     zNIC,
			NetdevID:  fmt.Sprintf("eth%d", zNIC),
			Interface: zIface,
			LinkPort:  port,
			Listen:    false,
			RemoteIP:  connectIP,
		})
	}

	return result, nil
}

// splitLinkEndpoint splits a "device:interface" string.
func splitLinkEndpoint(endpoint string) (string, string, error) {
	idx := strings.IndexByte(endpoint, ':')
	if idx < 0 {
		return "", "", fmt.Errorf("invalid endpoint format %q (expected device:interface)", endpoint)
	}
	return endpoint[:idx], endpoint[idx+1:], nil
}
