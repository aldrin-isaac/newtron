package main

import (
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// inprocSpecClient lets the co-located newtlab handler read newtron spec
// data without the HTTP loopback that bin/newtlab-server uses against a
// separate bin/newtron-server. It resolves the *newtron.Network from the
// in-process newtron api.Server at call time, mirroring the HTTP client's
// netID-bound semantics so newtlab.NewLab behaves identically in both
// composition modes.
//
// Issue #97 — replaces the prior newtronclient.Client wiring in
// cmd/newt-server. The loopback path deadlocked when a NetworkActor
// closure (e.g. /host/{name} resolving an SSH port through newtlab)
// triggered an inner GET that had to queue on the same actor goroutine.
// Reading the Network directly skips the actor and the network stack.
type inprocSpecClient struct {
	server *api.Server
	netID  string
}

func (c *inprocSpecClient) network() (interface {
	GetTopology() *spec.TopologySpecFile
	ListPlatforms() *spec.PlatformSpecFile
	ShowProfile(name string) (*spec.DeviceProfile, error)
}, error) {
	n := c.server.Network(c.netID)
	if n == nil {
		return nil, fmt.Errorf("network %q not registered", c.netID)
	}
	return n, nil
}

func (c *inprocSpecClient) GetTopology() (*spec.TopologySpecFile, error) {
	n, err := c.network()
	if err != nil {
		return nil, err
	}
	t := n.GetTopology()
	if t == nil {
		return nil, fmt.Errorf("network %q has no topology", c.netID)
	}
	return t, nil
}

func (c *inprocSpecClient) ListPlatforms() (*spec.PlatformSpecFile, error) {
	n, err := c.network()
	if err != nil {
		return nil, err
	}
	return n.ListPlatforms(), nil
}

func (c *inprocSpecClient) ShowProfile(name string) (*spec.DeviceProfile, error) {
	n, err := c.network()
	if err != nil {
		return nil, err
	}
	return n.ShowProfile(name)
}
