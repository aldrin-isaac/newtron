package newtron

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/newtron/network"
)

// Network is the top-level API entry point.
type Network struct {
	internal *network.Network
	auth     *auth.Checker
}

// LoadNetwork loads all spec files from specDir and returns a Network ready for use.
func LoadNetwork(specDir string) (*Network, error) {
	net, err := network.NewNetwork(specDir)
	if err != nil {
		return nil, err
	}
	return &Network{internal: net}, nil
}

// SetAuth installs a permission checker. If nil, all permission checks are skipped.
func (net *Network) SetAuth(checker *auth.Checker) {
	net.auth = checker
}

// Connect loads the named device and establishes a connection to its Redis databases.
func (net *Network) Connect(ctx context.Context, device string) (*Node, error) {
	dev, err := net.internal.ConnectNode(ctx, device)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", device, err)
	}
	return &Node{net: net, internal: dev}, nil
}

// ListNodes returns the names of all devices that have been loaded into this Network.
func (net *Network) ListNodes() []string {
	return net.internal.ListNodes()
}

// HasTopology returns true if a topology.json was loaded with the spec files.
func (net *Network) HasTopology() bool {
	return net.internal.HasTopology()
}

// TopologyDeviceNames returns the sorted device names from the topology.
// Returns nil if no topology is loaded.
func (net *Network) TopologyDeviceNames() []string {
	topo := net.internal.GetTopology()
	if topo == nil {
		return nil
	}
	return topo.DeviceNames()
}

// IsHostDevice returns true if the named device is a virtual host (not a SONiC switch).
func (net *Network) IsHostDevice(name string) bool {
	return net.internal.IsHostDevice(name)
}

// GetHostProfile returns connection parameters for a host device.
func (net *Network) GetHostProfile(name string) (*HostProfile, error) {
	p, err := net.internal.GetHostProfile(name)
	if err != nil {
		return nil, err
	}
	return &HostProfile{
		MgmtIP:  p.MgmtIP,
		SSHUser: p.SSHUser,
		SSHPass: p.SSHPass,
		SSHPort: p.SSHPort,
	}, nil
}

func (net *Network) checkPermission(perm auth.Permission, authCtx *auth.Context) error {
	if net.auth != nil {
		return net.auth.Check(perm, authCtx)
	}
	return nil
}
