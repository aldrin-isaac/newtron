package newtlab

import (
	"fmt"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ResolveNICIndex returns the NIC slot backing the named interface, looked up in
// the platform's explicit port inventory (PlatformSpec.Ports) — the single
// source of the device-native name → NIC mapping for every platform, switch and
// host alike (see docs/newtron/platform-port-model.md). NIC 0 is management and
// never appears in the table, so every resolved slot is >= 1.
//
// For a switch (a dedicated VM) the returned index is the absolute QEMU slot.
// For a coalesced host the returned index is the interface's per-host ordinal;
// AllocateLinks maps it to the shared VM's absolute slot as NICBase + (ord-1).
//
// Returns an actionable error when the name is not in the inventory — the
// caller (AllocateLinks) wraps it with the link/device context so a mis-typed
// or out-of-inventory topology interface fails at deploy time rather than
// producing a VM whose NIC maps to nothing.
func ResolveNICIndex(ports []spec.PortSpec, interfaceName string) (int, error) {
	if len(ports) == 0 {
		return 0, fmt.Errorf("newtlab: platform has no port inventory; cannot resolve %q", interfaceName)
	}
	for _, p := range ports {
		if p.Name == interfaceName {
			return p.NICIndex, nil
		}
	}
	return 0, fmt.Errorf("newtlab: interface %q is not in the platform port inventory (%d ports: %s)",
		interfaceName, len(ports), samplePortNames(ports))
}

// samplePortNames renders a compact, ordered preview of the inventory's port
// names for error messages — the first few plus the last when the list is long,
// so the operator can see the naming convention (stride-1 vs stride-4) without a
// wall of names.
func samplePortNames(ports []spec.PortSpec) string {
	const head = 4
	if len(ports) <= head+1 {
		names := make([]string, len(ports))
		for i, p := range ports {
			names[i] = p.Name
		}
		return strings.Join(names, ", ")
	}
	names := make([]string, 0, head+2)
	for i := 0; i < head; i++ {
		names = append(names, ports[i].Name)
	}
	names = append(names, "…", ports[len(ports)-1].Name)
	return strings.Join(names, ", ")
}
