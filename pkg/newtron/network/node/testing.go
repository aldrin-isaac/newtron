package node

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// NewNodeForTest creates a Node with pre-configured ConfigDB state for testing.
// This is the external-test seam: external test packages (e.g. node_test) that
// need a Node without connecting to real SONiC hardware use this constructor.
func NewNodeForTest(name string, configDB *sonic.ConfigDB, connected, locked bool) *Node {
	return &Node{
		name:       name,
		configDB:   configDB,
		connected:  connected,
		locked:     locked,
		interfaces: make(map[string]*Interface),
		resolved:   &spec.ResolvedProfile{DeviceName: name},
	}
}

// MarkActuatedForTest flips n.actuatedIntent so external tests can
// simulate the post-InitFromDeviceIntent state without an SSH-backed
// device. Production code reaches this state only through
// InitFromDeviceIntent.
func MarkActuatedForTest(n *Node) {
	n.actuatedIntent = true
}
