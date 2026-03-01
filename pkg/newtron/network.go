package newtron

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/newtron/network"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
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

// Abstract creates an offline abstract Node for the named device.
// The Node starts with an empty shadow ConfigDB — operations accumulate entries
// for composite export without requiring a physical device connection.
func (net *Network) Abstract(device string) (*Node, error) {
	dev, err := net.internal.GetAbstractNode(device)
	if err != nil {
		return nil, err
	}
	return &Node{net: net, internal: dev, abstract: true}, nil
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

// Spec returns the raw network spec. Used by auth.NewChecker.
func (net *Network) Spec() *spec.NetworkSpecFile {
	return net.internal.Spec()
}

// Internal returns the underlying network.Network for newtrun escape hatch.
func (net *Network) Internal() *network.Network {
	return net.internal
}

func (net *Network) checkPermission(perm auth.Permission, authCtx *auth.Context) error {
	if net.auth != nil {
		return net.auth.Check(perm, authCtx)
	}
	return nil
}
