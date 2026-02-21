// Package device — verification types for route observation and ChangeSet verification.
// These types support the v5 verification architecture: newtron observes single-device state
// and returns structured data; orchestrators (newtest) assert cross-device correctness.
package device

// RouteSource indicates which Redis database a route was read from.
type RouteSource string

const (
	RouteSourceAppDB  RouteSource = "APP_DB"
	RouteSourceAsicDB RouteSource = "ASIC_DB"
)

// RouteEntry represents a route read from a device's routing table.
// Returned by Device.GetRoute (APP_DB) and Device.GetRouteASIC (ASIC_DB).
type RouteEntry struct {
	Prefix   string      // "10.1.0.0/31"
	VRF      string      // "default", "Vrf-customer"
	Protocol string      // "bgp", "connected", "static"
	NextHops []NextHop
	Source   RouteSource // AppDB or AsicDB
}

// NextHop represents a single next-hop in a route entry.
type NextHop struct {
	IP        string // "10.0.0.1" (or "0.0.0.0" for connected)
	Interface string // "Ethernet0", "Vlan500"
}

// VerificationResult reports ChangeSet verification outcome.
// Returned by Device.VerifyChangeSet after re-reading CONFIG_DB.
type VerificationResult struct {
	Passed int                 // entries that matched
	Failed int                 // entries missing or mismatched
	Errors []VerificationError // details of each failure
}

// VerificationError describes a single verification failure.
type VerificationError struct {
	Table    string
	Key      string
	Field    string
	Expected string
	Actual   string // "" if missing
}

// NeighEntry represents a neighbor (ARP/NDP) entry read from a device.
// Returned by Node.GetNeighbor (STATE_DB NEIGH_TABLE).
type NeighEntry struct {
	IP        string // "10.20.0.1"
	Interface string // "Ethernet1", "Vlan100"
	MAC       string // "aa:bb:cc:dd:ee:ff"
	Family    string // "IPv4", "IPv6"
}
