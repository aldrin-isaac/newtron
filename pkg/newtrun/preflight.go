package newtrun

import (
	"fmt"
	"strings"
)

// preflightInterfaces validates that every interface a topology link references
// exists in that node's platform inventory (#403). It is a spec-level check
// against the same ports[] authority newtlab enforces at deploy and newtron
// enforces per operation — run before deploy so a mistyped or out-of-inventory
// interface (host or switch) fails at suite load, with one aggregated message,
// rather than deep in a deploy after VMs have started.
//
// A network with no topology or no links (e.g. a config-only loopback suite)
// has nothing to validate and passes.
func (r *Runner) preflightInterfaces() error {
	topo, err := r.Client.GetTopology()
	if err != nil || topo == nil {
		return nil
	}

	// Cache each node's supported interface names — one inventory fetch per node.
	supported := map[string]map[string]bool{}
	inventory := func(device string) (map[string]bool, error) {
		if s, ok := supported[device]; ok {
			return s, nil
		}
		entries, err := r.Client.ListInterfaces(device)
		if err != nil {
			return nil, err
		}
		s := make(map[string]bool, len(entries))
		for _, e := range entries {
			s[e.Name] = true
		}
		supported[device] = s
		return s, nil
	}

	var problems []string
	for _, l := range topo.Links {
		for _, ep := range []string{l.A, l.Z} {
			device, iface, ok := splitTopologyEndpoint(ep)
			if !ok {
				problems = append(problems, fmt.Sprintf("malformed link endpoint %q (want \"device:interface\")", ep))
				continue
			}
			names, err := inventory(device)
			if err != nil {
				return fmt.Errorf("newtrun: interface pre-flight: reading inventory for %q: %w", device, err)
			}
			if !names[iface] {
				problems = append(problems,
					fmt.Sprintf("%s: interface %q is not in the platform inventory", device, iface))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("newtrun: topology interface pre-flight failed (%d problem(s)):\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
	return nil
}

// splitTopologyEndpoint splits a "device:interface" topology endpoint.
func splitTopologyEndpoint(endpoint string) (device, iface string, ok bool) {
	parts := strings.SplitN(endpoint, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
