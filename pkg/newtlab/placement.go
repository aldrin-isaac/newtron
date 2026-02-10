package newtlab

import (
	"fmt"
	"sort"

	"github.com/newtron-network/newtron/pkg/spec"
)

// serverLoad tracks current node count for a server during placement.
type serverLoad struct {
	server *spec.ServerConfig
	count  int
}

// PlaceNodes assigns unpinned nodes to servers using a spread algorithm
// that minimizes maximum load across servers. Pinned nodes (Host != "")
// are validated against the server list and count toward capacity.
//
// If servers is empty, PlaceNodes is a no-op (single-host mode).
func PlaceNodes(nodes map[string]*NodeConfig, servers []*spec.ServerConfig) error {
	if len(servers) == 0 {
		return nil
	}

	// Build server load tracking
	loads := make([]*serverLoad, len(servers))
	serverIndex := make(map[string]*serverLoad, len(servers))
	for i, s := range servers {
		loads[i] = &serverLoad{server: s}
		serverIndex[s.Name] = loads[i]
	}

	// Phase 1: Validate and count pinned nodes (sorted for determinism)
	sortedNames := sortedNodeNames(nodes)
	for _, name := range sortedNames {
		node := nodes[name]
		if node.Host == "" {
			continue
		}
		sl, ok := serverIndex[node.Host]
		if !ok {
			return fmt.Errorf("newtlab: node %s pinned to unknown server %q", name, node.Host)
		}
		if sl.server.MaxNodes > 0 && sl.count >= sl.server.MaxNodes {
			return fmt.Errorf("newtlab: server %q over capacity (max_nodes=%d) with pinned node %s",
				node.Host, sl.server.MaxNodes, name)
		}
		sl.count++
	}

	// Phase 2: Place unpinned nodes using spread (minimize max load)
	for _, name := range sortedNames {
		node := nodes[name]
		if node.Host != "" {
			continue
		}

		// Sort servers by (count asc, name asc) for deterministic spread
		sort.Slice(loads, func(i, j int) bool {
			if loads[i].count != loads[j].count {
				return loads[i].count < loads[j].count
			}
			return loads[i].server.Name < loads[j].server.Name
		})

		placed := false
		for _, sl := range loads {
			if sl.server.MaxNodes > 0 && sl.count >= sl.server.MaxNodes {
				continue
			}
			node.Host = sl.server.Name
			sl.count++
			placed = true
			break
		}
		if !placed {
			return fmt.Errorf("newtlab: no server capacity for node %s (all servers full)", name)
		}
	}

	return nil
}
