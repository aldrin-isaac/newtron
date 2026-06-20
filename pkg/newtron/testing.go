package newtron

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
)

// MarkActuatedForTest flips the wrapped node's actuatedIntent flag so
// external tests can simulate the post-InitFromDeviceIntent state
// without an SSH-backed device. Mirrors node.MarkActuatedForTest at
// the public-API layer; production code reaches this state only
// through InitFromDeviceIntent.
func MarkActuatedForTest(n *Node) {
	node.MarkActuatedForTest(n.internal)
}
