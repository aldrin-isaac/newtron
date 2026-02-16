package newtlab

import (
	"fmt"
	"strconv"
	"strings"
)

// ResolveNICIndex returns the QEMU NIC index for a SONiC interface name
// using the given interface map scheme. When interfaceMap is "custom",
// the customMap parameter is used for direct lookup; otherwise it is ignored.
//
// NIC 0 is always management — data NICs start at index 1.
func ResolveNICIndex(interfaceMap, interfaceName string, customMap map[string]int) (int, error) {
	switch interfaceMap {
	case "sequential":
		idx := parseEthernetIndex(interfaceName)
		if idx < 0 {
			return 0, fmt.Errorf("newtlab: invalid interface name %q for sequential map", interfaceName)
		}
		return idx + 1, nil

	case "stride-4":
		idx := parseEthernetIndex(interfaceName)
		if idx < 0 {
			return 0, fmt.Errorf("newtlab: invalid interface name %q for stride-4 map", interfaceName)
		}
		if idx%4 != 0 {
			return 0, fmt.Errorf("newtlab: interface %q index %d not divisible by 4 for stride-4 map", interfaceName, idx)
		}
		return idx/4 + 1, nil

	case "linux":
		// eth1 → NIC 1, eth2 → NIC 2 (eth0 = NIC 0 = mgmt, never in links)
		idx := parseLinuxEthIndex(interfaceName)
		if idx < 0 {
			return 0, fmt.Errorf("newtlab: invalid interface name %q for linux map", interfaceName)
		}
		return idx, nil

	case "custom":
		if customMap == nil {
			return 0, fmt.Errorf("newtlab: custom interface map requires vm_interface_map_custom")
		}
		nicIdx, ok := customMap[interfaceName]
		if !ok {
			return 0, fmt.Errorf("newtlab: interface %q not found in custom map", interfaceName)
		}
		return nicIdx, nil

	default:
		return 0, fmt.Errorf("newtlab: unknown interface map %q", interfaceMap)
	}
}

// ResolveInterfaceName returns the SONiC interface name for a QEMU NIC index.
// Inverse of ResolveNICIndex. When interfaceMap is "custom", the customMap
// parameter is used for reverse lookup; otherwise it is ignored.
func ResolveInterfaceName(interfaceMap string, nicIndex int, customMap map[string]int) string {
	switch interfaceMap {
	case "sequential":
		return fmt.Sprintf("Ethernet%d", nicIndex-1)

	case "stride-4":
		return fmt.Sprintf("Ethernet%d", (nicIndex-1)*4)

	case "linux":
		return fmt.Sprintf("eth%d", nicIndex)

	case "custom":
		for name, idx := range customMap {
			if idx == nicIndex {
				return name
			}
		}
		return fmt.Sprintf("NIC%d", nicIndex)

	default:
		return fmt.Sprintf("NIC%d", nicIndex)
	}
}

// parseEthernetIndex extracts the numeric index from "EthernetN".
// Returns -1 if the name is not a valid Ethernet interface.
func parseEthernetIndex(name string) int {
	if !strings.HasPrefix(name, "Ethernet") {
		return -1
	}
	n, err := strconv.Atoi(name[len("Ethernet"):])
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// parseLinuxEthIndex extracts the numeric index from "ethN".
// Returns -1 if the name is not a valid Linux ethernet interface.
func parseLinuxEthIndex(name string) int {
	if !strings.HasPrefix(name, "eth") {
		return -1
	}
	n, err := strconv.Atoi(name[len("eth"):])
	if err != nil || n < 0 {
		return -1
	}
	return n
}
