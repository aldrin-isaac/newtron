package util

import (
	"regexp"
	"strings"
)

// DerivedValues contains auto-computed values from user input
type DerivedValues struct {
	NeighborIP    string // Computed from local IP for point-to-point
	NetworkAddr   string // Network address of the subnet
	BroadcastAddr string // Broadcast address (for non-p2p)
	SubnetMask    int    // Subnet mask length
	VRFName       string // Auto-generated VRF name
	Description   string // Auto-generated description
	ACLNameBase   string // Base name for ACLs
}

// DeriveFromInterface derives values from interface name, IP, and service name
func DeriveFromInterface(intf, ipWithMask, serviceName string) (*DerivedValues, error) {
	d := &DerivedValues{}

	if ipWithMask != "" {
		ip, mask, err := ParseIPWithMask(ipWithMask)
		if err != nil {
			return nil, err
		}

		d.SubnetMask = mask
		d.NetworkAddr = ComputeNetworkAddr(ip.String(), mask)
		d.BroadcastAddr = ComputeBroadcastAddr(ip.String(), mask)
		d.NeighborIP = ComputeNeighborIP(ip.String(), mask)
	}

	// Generate VRF name from service and interface (using short names)
	shortIntf := SanitizeForName(ShortenInterfaceName(intf))
	d.VRFName = serviceName + "-" + shortIntf

	// Generate ACL base name (using short names)
	d.ACLNameBase = serviceName + "-" + shortIntf

	return d, nil
}

// SanitizeForName converts an interface name to a valid identifier
// Ethernet0 -> Ethernet0, PortChannel100 -> PortChannel100, Ethernet0.100 -> Ethernet0_100
func SanitizeForName(name string) string {
	// Replace dots and slashes with underscores
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, "/", "_")
	// Remove any other special characters
	reg := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	return reg.ReplaceAllString(name, "")
}

// DeriveVRFName generates a VRF name based on type
// Uses short interface names: customer-edge-Eth0 instead of customer-edge-Ethernet0
func DeriveVRFName(vrfType, serviceName, interfaceName string) string {
	switch vrfType {
	case "interface":
		return serviceName + "-" + SanitizeForName(ShortenInterfaceName(interfaceName))
	case "shared":
		return serviceName
	default:
		return serviceName + "-" + SanitizeForName(ShortenInterfaceName(interfaceName))
	}
}

// DeriveACLName generates an ACL name for a service and direction.
// ACLs are per-service, not per-interface: customer-edge-in, customer-edge-out
// Multiple interfaces using the same service share the same ACL.
func DeriveACLName(serviceName, direction string) string {
	return serviceName + "-" + direction
}

// DeriveRouterID returns the router ID, typically from loopback IP
func DeriveRouterID(loopbackIP string) string {
	return loopbackIP
}

// DeriveVTEPSourceIP returns the VTEP source IP from loopback
func DeriveVTEPSourceIP(loopbackIP string) string {
	return loopbackIP
}

// DeriveRouteDistinguisher generates an RD for a VRF
func DeriveRouteDistinguisher(routerID string, vrfIndex int) string {
	return FormatRouteDistinguisher(routerID, vrfIndex)
}

// IsPointToPoint returns true if the mask length indicates a p2p link
func IsPointToPoint(maskLen int) bool {
	return maskLen == 30 || maskLen == 31
}

// ParseInterfaceName extracts interface type and number
// Returns (type, number, subinterface) e.g., ("Ethernet", "0", "100") for Ethernet0.100
func ParseInterfaceName(name string) (ifType string, num string, subintf string) {
	// Check for subinterface
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		subintf = parts[1]
		name = parts[0]
	}

	// Extract type and number
	reg := regexp.MustCompile(`^([a-zA-Z]+)(\d+(?:/\d+)*)$`)
	matches := reg.FindStringSubmatch(name)
	if len(matches) == 3 {
		return matches[1], matches[2], subintf
	}

	return name, "", subintf
}

// Interface name mappings (long <-> short)
var (
	// longToShort maps full interface type names to abbreviations
	longToShort = map[string]string{
		"Ethernet":    "Eth",
		"PortChannel": "Po",
		"Loopback":    "Lo",
		"Vlan":        "Vl",
		"Management":  "Mgmt",
	}

	// shortToLong maps abbreviations to full interface type names
	shortToLong = map[string]string{
		"eth":  "Ethernet",
		"po":   "PortChannel",
		"lo":   "Loopback",
		"vl":   "Vlan",
		"vlan": "Vlan",
		"mgmt": "Management",
	}
)

// ShortenInterfaceName converts a full interface name to short form
// Ethernet0 -> Eth0, PortChannel100 -> Po100, Loopback0 -> Lo0, Vlan100 -> Vl100
func ShortenInterfaceName(name string) string {
	ifType, num, subintf := ParseInterfaceName(name)

	if short, ok := longToShort[ifType]; ok {
		result := short + num
		if subintf != "" {
			result += "." + subintf
		}
		return result
	}

	// No mapping found, return sanitized original
	return SanitizeForName(name)
}

// ExpandInterfaceName converts a short interface name to full SONiC format
// Eth0 -> Ethernet0, Po100 -> PortChannel100, Lo0 -> Loopback0, Vl100 -> Vlan100
func ExpandInterfaceName(name string) string {
	return NormalizeInterfaceName(name)
}

// NormalizeInterfaceName normalizes interface names to SONiC format
// eth0 -> Ethernet0, po100 -> PortChannel100, etc.
func NormalizeInterfaceName(name string) string {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)

	for abbr, full := range shortToLong {
		if strings.HasPrefix(lower, abbr) && len(name) > len(abbr) {
			suffix := name[len(abbr):]
			// Check if suffix starts with a digit
			if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
				return full + suffix
			}
		}
	}

	// Already in correct format or unknown
	return name
}

// CoalesceInt returns the first non-zero int
func CoalesceInt(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// MergeMaps merges maps with later maps overriding earlier ones
func MergeMaps[K comparable, V any](maps ...map[K]V) map[K]V {
	result := make(map[K]V)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// MergeStringSlices merges string slices, removing duplicates
func MergeStringSlices(slices ...[]string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, slice := range slices {
		for _, s := range slice {
			if !seen[s] {
				seen[s] = true
				result = append(result, s)
			}
		}
	}
	return result
}
