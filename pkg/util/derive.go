package util

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	sanitizeRegexp       = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	parseInterfaceRegexp = regexp.MustCompile(`^([a-zA-Z]+)(\d+(?:/\d+)*)$`)
)

// NormalizeName converts a user-provided spec name to canonical form for
// CONFIG_DB key construction. Hyphens → underscores, then uppercased.
// Called by the spec loader on all name keys and name-reference fields at
// load time. Operations code should never need to call this directly.
func NormalizeName(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// NormalizeVRFName normalizes a CONFIG_DB VRF name. Preserves the "Vrf_" prefix
// (required by SONiC CONFIG_DB convention) and normalizes the suffix to uppercase
// with underscores. Examples: "Vrf_irb" → "Vrf_IRB", "Vrf_l3evpn" → "Vrf_L3EVPN".
func NormalizeVRFName(name string) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "Vrf_") {
		return "Vrf_" + NormalizeName(name[4:])
	}
	// If no Vrf_ prefix, add it and normalize the whole thing
	return "Vrf_" + NormalizeName(name)
}

// DerivedValues contains auto-computed values from user input
type DerivedValues struct {
	NeighborIP  string // Computed from local IP for point-to-point
	Description string // Auto-generated description
	ACLPrefix   string // Prefix for per-interface ACL names (append "-in"/"-out")
}

// DeriveFromInterface derives values from interface name, IP, and service name.
// Service names are expected to already be normalized (uppercase, underscores).
func DeriveFromInterface(intf, ipWithMask, serviceName string) (*DerivedValues, error) {
	d := &DerivedValues{}

	if ipWithMask != "" {
		ip, mask, err := parseIPWithMask(ipWithMask)
		if err != nil {
			return nil, err
		}

		d.NeighborIP = ComputeNeighborIP(ip.String(), mask)
	}

	// Generate ACL base name (using short names, uppercase)
	shortIntf := strings.ToUpper(SanitizeForName(ShortenInterfaceName(intf)))
	d.ACLPrefix = serviceName + "_" + shortIntf

	return d, nil
}

// SanitizeForName converts an interface name to a valid identifier
// Ethernet0 -> Ethernet0, PortChannel100 -> PortChannel100, Ethernet0.100 -> Ethernet0_100
func SanitizeForName(name string) string {
	// Replace dots and slashes with underscores
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, "/", "_")
	// Remove any other special characters
	return sanitizeRegexp.ReplaceAllString(name, "")
}

const (
	// VRFNameMaxLen is the usable Linux interface/VRF device-name length
	// (IFNAMSIZ = 16 → 15 usable). A derived VRF name longer than this makes
	// vrfmgrd's `ip link add <name> type vrf` FAIL, so the kernel VRF is never
	// created — intfmgrd then can't program the SVI's IP and the dataplane dies.
	// This is invisible to CONFIG_DB/drift/snapshot (the row exists there); it
	// only surfaces in the dataplane. So the derivation stays within the limit,
	// and the length is enforced fail-closed at author time and apply time.
	VRFNameMaxLen = 15
	vrfPrefix     = "Vrf_"
	shortIfaceMax = 5 // budget reserved for the shortened interface (E12, P1, V400)

	// MaxSharedServiceName / MaxInterfaceServiceName are the author-time service
	// name budgets DERIVED from VRFNameMaxLen — not independent magic numbers:
	//   shared:    "Vrf_" + name            ≤ 15 → name ≤ 11
	//   interface: "Vrf_" + name + "_" + I  ≤ 15 → name ≤ 5 (I ≤ 5)
	MaxSharedServiceName    = VRFNameMaxLen - len(vrfPrefix)                    // 11
	MaxInterfaceServiceName = VRFNameMaxLen - len(vrfPrefix) - 1 - shortIfaceMax // 5
)

// MaxServiceNameLen returns the author-time service-name budget for a vrf_type,
// so the schema/CLI can cap the name field and validateConstraints can reject an
// over-long name before it ever derives an oversized VRF name. A service with no
// VRF (bridged/evpn-bridged, or routed in the default VRF) has no derived VRF
// name — callers skip the check (returns MaxSharedServiceName as a safe default).
func MaxServiceNameLen(vrfType string) int {
	if vrfType == "interface" {
		return MaxInterfaceServiceName
	}
	return MaxSharedServiceName
}

// ifaceVRFLetter collapses an interface type to a single letter for VRF naming —
// the tightest budget (interface-mode VRFs) needs the interface as short as
// possible: Ethernet12→E12, PortChannel1→P1, Vlan400→V400.
var ifaceVRFLetter = map[string]string{
	"Ethernet": "E", "PortChannel": "P", "Vlan": "V", "Loopback": "L", "Management": "M",
}

// shortenInterfaceForVRF renders an interface as its single-letter VRF form.
func shortenInterfaceForVRF(name string) string {
	ifType, num, subintf := ParseInterfaceName(name)
	letter, ok := ifaceVRFLetter[ifType]
	if !ok {
		return strings.ToUpper(SanitizeForName(name))
	}
	out := letter + num
	if subintf != "" {
		out += "_" + subintf
	}
	return strings.ToUpper(out)
}

// DeriveVRFName generates the on-device SONiC VRF name for a service's own VRF.
// The VRF is named after the service — the invariant — not the IP-VPN it joins
// (a mutable binding). Service names are expected to already be normalized
// (uppercase, underscores).
//
//   - shared:    "Vrf_<SERVICE>"           — one VRF shared across the service's
//                                             interfaces (e.g. "Vrf_OVERLAY_A").
//   - interface: "Vrf_<SERVICE>_<IFACE>"   — one VRF per interface, the interface
//                                             single-lettered (e.g. "Vrf_IRB_E2").
//
// Both must fit VRFNameMaxLen; the service-name budgets (MaxServiceNameLen) keep
// them there, and ValidateVRFNameLength is the fail-closed backstop for the runtime
// interface edge (a 5-digit port or a sub-interface that overruns the 5-char
// interface budget). If the service joins an IP-VPN, that VPN's L3VNI/route-targets
// bind onto this VRF (BindIPVPN); inter-service connectivity is by the shared L3VNI
// across the fabric, independent of the device-local VRF name.
//
// The "Vrf_" prefix is mandatory, not cosmetic: intfmgrd silently drops INTERFACE
// rows whose vrf_name lacks it (RCA-044). (A VRF authored via `vrf bind-ipvpn`
// gets the prefix from NormalizeVRFName instead; an IP-VPN has no derived VRF name
// of its own — VRFs join a VPN, they are not named after it.)
func DeriveVRFName(vrfType, serviceName, interfaceName string) string {
	if vrfType == "shared" {
		return vrfPrefix + serviceName
	}
	return vrfPrefix + serviceName + "_" + shortenInterfaceForVRF(interfaceName)
}

// ValidateVRFNameLength is the authoritative fail-closed guard: a derived VRF
// name over VRFNameMaxLen must be rejected before it reaches the device, where
// vrfmgrd would silently fail to create the kernel VRF. The service-name budgets
// prevent this at author time; this catches the apply-time interface edge.
func ValidateVRFNameLength(vrfName string) error {
	if len(vrfName) > VRFNameMaxLen {
		return fmt.Errorf("VRF name %q is %d characters — exceeds the %d-char Linux interface-name limit (IFNAMSIZ); the kernel VRF would silently fail to create. Use a shorter service name",
			vrfName, len(vrfName), VRFNameMaxLen)
	}
	return nil
}

// DeriveACLName generates a content-hashed ACL name from filter name, direction,
// and content hash. The hash ensures that different filter contents produce different
// ACL names, enabling sharing: two services using the same filter share one ACL.
//
// Format: FILTERNAME_DIRECTION_HASH (e.g., PROTECT_RE_IN_A1B2C3D4)
// All inputs are expected to be pre-normalized (uppercase, underscores).
func DeriveACLName(filterName, direction, contentHash string) string {
	return filterName + "_" + strings.ToUpper(direction) + "_" + contentHash
}

// ContentHash computes an 8-character uppercase hex hash from CONFIG_DB entry
// field maps. Used for content-addressed naming of shared policy objects (ACLs,
// route maps, prefix sets). Entries are sorted by key for determinism.
//
// Per DESIGN_PRINCIPLES_NEWTRON.md §16 (Content-Hashed Naming): the hash is computed from the actual
// CONFIG_DB fields that would be written, not the spec struct.
func ContentHash(entries []map[string]string) string {
	h := sha256.New()
	for _, fields := range entries {
		// Sort field keys for deterministic ordering
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			h.Write([]byte(k))
			h.Write([]byte("="))
			h.Write([]byte(fields[k]))
			h.Write([]byte("\n"))
		}
		h.Write([]byte("---\n"))
	}
	return fmt.Sprintf("%X", h.Sum(nil)[:4])
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
	matches := parseInterfaceRegexp.FindStringSubmatch(name)
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

	// shortToLongSorted contains abbreviation keys sorted longest-first
	// so that "vlan" is matched before "vl" in NormalizeInterfaceName.
	shortToLongSorted []string
)

func init() {
	shortToLongSorted = make([]string, 0, len(shortToLong))
	for k := range shortToLong {
		shortToLongSorted = append(shortToLongSorted, k)
	}
	sort.Slice(shortToLongSorted, func(i, j int) bool {
		return len(shortToLongSorted[i]) > len(shortToLongSorted[j])
	})
}

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

// NormalizeInterfaceName normalizes interface names to SONiC format
// eth0 -> Ethernet0, po100 -> PortChannel100, etc.
func NormalizeInterfaceName(name string) string {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)

	for _, abbr := range shortToLongSorted {
		if strings.HasPrefix(lower, abbr) && len(name) > len(abbr) {
			suffix := name[len(abbr):]
			if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
				return shortToLong[abbr] + suffix
			}
		}
	}

	// Already in correct format or unknown
	return name
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

