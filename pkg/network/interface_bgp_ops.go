package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Direct BGP Neighbor Operations (Interface-level, uses link IP as source)
// ============================================================================
// These operations are for eBGP neighbors where the BGP session is sourced
// from the interface IP (direct peering over a link).
//
// For iBGP neighbors using loopback IPs (indirect peering), use the
// device-level BGP operations: Device.AddLoopbackBGPNeighbor() or
// Device.SetupBGPEVPN().

// DirectBGPNeighborConfig holds configuration for a direct BGP neighbor.
type DirectBGPNeighborConfig struct {
	NeighborIP  string // Neighbor IP (auto-derived for /30, /31 if empty)
	RemoteAS    int    // Remote AS number (required for eBGP)
	Description string // Optional description
	Password    string // Optional MD5 password
	BFD         bool   // Enable BFD for fast failure detection
	Multihop    int    // eBGP multihop TTL (0 = directly connected)
}

// AddBGPNeighbor adds a direct BGP neighbor on this interface.
// The BGP session will use this interface's IP as the update-source.
// For point-to-point links (/30, /31), the neighbor IP is auto-derived if not specified.
func (i *Interface) AddBGPNeighbor(ctx context.Context, cfg DirectBGPNeighborConfig) (*ChangeSet, error) {
	d := i.device

	if err := requireWritable(d); err != nil {
		return nil, err
	}
	if cfg.RemoteAS == 0 {
		return nil, fmt.Errorf("remote AS number is required")
	}

	// Interface must have an IP address
	if len(i.ipAddresses) == 0 {
		return nil, fmt.Errorf("interface %s has no IP address configured", i.name)
	}

	// Get the interface's IP address (use first one)
	localIP := i.ipAddresses[0]

	// Auto-derive neighbor IP for point-to-point links if not specified
	neighborIP := cfg.NeighborIP
	if neighborIP == "" {
		derivedIP, err := util.DeriveNeighborIP(localIP)
		if err != nil {
			return nil, fmt.Errorf("cannot auto-derive neighbor IP from %s: %v (specify neighbor IP explicitly)", localIP, err)
		}
		neighborIP = derivedIP
	}

	// Validate neighbor IP
	if !util.IsValidIPv4(neighborIP) {
		return nil, fmt.Errorf("invalid neighbor IP: %s", neighborIP)
	}

	// Check if neighbor already exists
	if d.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s already exists", neighborIP)
	}

	cs := NewChangeSet(d.Name(), "interface.add-bgp-neighbor")

	// Extract local IP without mask for update-source
	localIPOnly, _ := util.SplitIPMask(localIP)

	// Add BGP neighbor entry
	fields := map[string]string{
		"asn":          fmt.Sprintf("%d", cfg.RemoteAS),
		"admin_status": "up",
		"local_addr":   localIPOnly, // Update source = interface IP
	}
	if cfg.Description != "" {
		fields["name"] = cfg.Description
	}
	if cfg.Multihop > 0 {
		fields["ebgp_multihop"] = fmt.Sprintf("%d", cfg.Multihop)
	}
	// Note: Password and BFD would be configured separately in SONiC

	// Key format: vrf|neighborIP (per SONiC Unified FRR Mgmt schema)
	cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeAdd, nil, fields)

	// Activate IPv4 unicast for this neighbor
	afKey := fmt.Sprintf("default|%s|ipv4_unicast", neighborIP)
	cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeAdd, nil, map[string]string{
		"activate": "true",
	})

	util.WithDevice(d.Name()).Infof("Adding direct BGP neighbor %s (AS %d) on interface %s",
		neighborIP, cfg.RemoteAS, i.name)
	return cs, nil
}

// RemoveBGPNeighbor removes a direct BGP neighbor from this interface.
func (i *Interface) RemoveBGPNeighbor(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	d := i.device

	if err := requireWritable(d); err != nil {
		return nil, err
	}

	// If no neighbor IP specified, try to derive it
	if neighborIP == "" && len(i.ipAddresses) > 0 {
		var err error
		neighborIP, err = util.DeriveNeighborIP(i.ipAddresses[0])
		if err != nil {
			return nil, fmt.Errorf("specify neighbor IP to remove")
		}
	}

	// Check neighbor exists
	if !d.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s not found", neighborIP)
	}

	cs := NewChangeSet(d.Name(), "interface.remove-bgp-neighbor")

	// Remove address-family entries first
	// Key format: vrf|neighborIP|af (per SONiC Unified FRR Mgmt schema)
	for _, af := range []string{"ipv4_unicast", "ipv6_unicast", "l2vpn_evpn"} {
		afKey := fmt.Sprintf("default|%s|%s", neighborIP, af)
		cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeDelete, nil, nil)
	}

	// Remove neighbor entry
	cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeDelete, nil, nil)

	util.WithDevice(d.Name()).Infof("Removing direct BGP neighbor %s from interface %s", neighborIP, i.name)
	return cs, nil
}

// GetBGPNeighborIP returns the BGP neighbor IP for this interface (if any).
// Returns empty string if no BGP neighbor is configured on this interface.
func (i *Interface) GetBGPNeighborIP() string {
	d := i.device
	if d.ConfigDB() == nil || len(i.ipAddresses) == 0 {
		return ""
	}

	localIP, _ := util.SplitIPMask(i.ipAddresses[0])

	// Find a BGP neighbor that uses this interface's IP as local_addr
	for neighborIP, neighbor := range d.ConfigDB().BGPNeighbor {
		if neighbor.LocalAddr == localIP {
			return neighborIP
		}
	}

	return ""
}

// ============================================================================
// v3: Route Map Binding
// ============================================================================

// SetRouteMap binds a route-map to a BGP neighbor's address-family (in/out direction).
// Used to apply import/export policies from the service routing spec.
func (i *Interface) SetRouteMap(ctx context.Context, neighborIP, af, direction, routeMapName string) (*ChangeSet, error) {
	d := i.device

	if err := requireWritable(d); err != nil {
		return nil, err
	}
	if direction != "in" && direction != "out" {
		return nil, fmt.Errorf("direction must be 'in' or 'out'")
	}

	// Verify neighbor exists
	if !d.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP neighbor %s not found", neighborIP)
	}

	// Verify route-map exists in CONFIG_DB
	configDB := d.ConfigDB()
	if configDB != nil {
		found := false
		prefix := routeMapName + "|"
		for key := range configDB.RouteMap {
			if key == routeMapName || len(key) > len(prefix) && key[:len(prefix)] == prefix {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("route-map '%s' not found in CONFIG_DB", routeMapName)
		}
	}

	cs := NewChangeSet(d.Name(), "interface.set-route-map")

	// Key format: vrf|neighborIP|af (per SONiC Unified FRR Mgmt schema)
	afKey := fmt.Sprintf("default|%s|%s", neighborIP, af)
	field := "route_map_in"
	if direction == "out" {
		field = "route_map_out"
	}

	cs.Add("BGP_NEIGHBOR_AF", afKey, ChangeModify, nil, map[string]string{
		field: routeMapName,
	})

	util.WithDevice(d.Name()).Infof("Set route-map %s %s on neighbor %s AF %s",
		routeMapName, direction, neighborIP, af)
	return cs, nil
}

// ============================================================================
// BGP Neighbor Helpers
// ============================================================================

// BGPNeighborConfig holds configuration for adding a BGP neighbor.
type BGPNeighborConfig struct {
	NeighborIP  string // Neighbor IP address
	RemoteASN   int    // Remote AS number
	Passive     bool   // Passive mode (wait for incoming connection)
	TTL         int    // eBGP multihop TTL
	Description string // Optional description
}

// DeriveNeighborIP derives the BGP neighbor IP from this interface's IP address.
// Only works for point-to-point links (/30 or /31 subnets).
func (i *Interface) DeriveNeighborIP() (string, error) {
	if len(i.ipAddresses) == 0 {
		return "", fmt.Errorf("interface %s has no IP address", i.name)
	}
	return util.DeriveNeighborIP(i.ipAddresses[0])
}

// AddBGPNeighborWithConfig adds a BGP neighbor using the provided config.
// This is an alternative to AddBGPNeighbor with more options.
func (i *Interface) AddBGPNeighborWithConfig(ctx context.Context, cfg BGPNeighborConfig) (*ChangeSet, error) {
	// Convert to DirectBGPNeighborConfig and delegate
	directCfg := DirectBGPNeighborConfig{
		NeighborIP:  cfg.NeighborIP,
		RemoteAS:    cfg.RemoteASN,
		Description: cfg.Description,
		Multihop:    cfg.TTL,
	}

	// Handle passive mode - note: SONiC may not support this directly
	// For now, we'll create the neighbor but it won't be passive
	if cfg.Passive && cfg.NeighborIP == "" {
		return nil, fmt.Errorf("passive mode requires a neighbor IP in SONiC")
	}

	return i.AddBGPNeighbor(ctx, directCfg)
}

// Ensure strings import is used.
var _ = strings.HasPrefix
