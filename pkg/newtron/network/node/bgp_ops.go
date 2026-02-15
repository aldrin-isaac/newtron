package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// v3: BGP Management Operations (frrcfgd)
// ============================================================================

// BGPGlobalsConfig holds configuration for SetBGPGlobals.
type BGPGlobalsConfig struct {
	VRF                string // VRF name ("default" for global)
	LocalASN           int    // Local AS number
	RouterID           string // Router ID (typically loopback IP)
	LoadBalanceMPRelax bool   // Enable multipath relax for ECMP
	RRClusterID        string // Route reflector cluster ID
	EBGPRequiresPolicy bool   // Require policy for eBGP (FRR 8.x default)
	DefaultIPv4Unicast bool   // Auto-activate IPv4 unicast
	LogNeighborChanges bool   // Log neighbor state changes
	SuppressFIBPending bool   // Suppress routes until FIB confirmed
}

// SetBGPGlobals configures BGP global settings via CONFIG_DB (frrcfgd).
func (n *Node) SetBGPGlobals(ctx context.Context, cfg BGPGlobalsConfig) (*ChangeSet, error) {
	vrf := cfg.VRF
	if vrf == "" {
		vrf = "default"
	}
	if err := n.precondition("set-bgp-globals", vrf).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.set-bgp-globals")

	fields := map[string]string{
		"local_asn": fmt.Sprintf("%d", cfg.LocalASN),
		"router_id": cfg.RouterID,
	}

	if cfg.LoadBalanceMPRelax {
		fields["load_balance_mp_relax"] = "true"
	}
	if cfg.RRClusterID != "" {
		fields["rr_cluster_id"] = cfg.RRClusterID
	}
	if !cfg.EBGPRequiresPolicy {
		fields["ebgp_requires_policy"] = "false"
	}
	if !cfg.DefaultIPv4Unicast {
		fields["default_ipv4_unicast"] = "false"
	}
	if cfg.LogNeighborChanges {
		fields["log_neighbor_changes"] = "true"
	}
	if cfg.SuppressFIBPending {
		fields["suppress_fib_pending"] = "true"
	}

	cs.Add("BGP_GLOBALS", vrf, ChangeAdd, nil, fields)

	util.WithDevice(n.Name()).Infof("Set BGP globals for VRF %s (ASN %d)", vrf, cfg.LocalASN)
	return cs, nil
}

// SetupRouteReflectorConfig holds configuration for SetupRouteReflector.
type SetupRouteReflectorConfig struct {
	Neighbors    []string // Neighbor loopback IPs
	ClusterID    string   // RR cluster ID (defaults to local loopback)
	MaxIBGPPaths int      // Max iBGP ECMP paths (default 2)
}

// SetupRouteReflector performs full route reflector setup with all 3 AFs
// (ipv4_unicast, ipv6_unicast, l2vpn_evpn). Replaces the v2 SetupBGPEVPN
// with comprehensive multi-AF route reflection.
func (n *Node) SetupRouteReflector(ctx context.Context, cfg SetupRouteReflectorConfig) (*ChangeSet, error) {
	if err := n.precondition("setup-route-reflector", "bgp").Result(); err != nil {
		return nil, err
	}

	resolved := n.Resolved()
	if resolved == nil {
		return nil, fmt.Errorf("device has no resolved profile")
	}

	cs := NewChangeSet(n.Name(), "device.setup-route-reflector")

	// Determine cluster ID
	clusterID := cfg.ClusterID
	if clusterID == "" {
		clusterID = resolved.LoopbackIP // Default to spine's loopback
	}

	// BGP_GLOBALS "default"
	cs.Add("BGP_GLOBALS", "default", ChangeAdd, nil, map[string]string{
		"local_asn":              fmt.Sprintf("%d", resolved.ASNumber),
		"router_id":             resolved.RouterID,
		"rr_cluster_id":         clusterID,
		"load_balance_mp_relax": "true",
		"ebgp_requires_policy":  "false",
		"log_neighbor_changes":  "true",
	})

	// Configure each neighbor with all 3 AFs
	// Key format: vrf|neighborIP (per SONiC Unified FRR Mgmt schema)
	for _, neighborIP := range cfg.Neighbors {
		// BGP_NEIGHBOR
		cs.Add("BGP_NEIGHBOR", fmt.Sprintf("default|%s", neighborIP), ChangeAdd, nil, map[string]string{
			"asn":          fmt.Sprintf("%d", resolved.ASNumber),
			"local_addr":   resolved.LoopbackIP,
			"admin_status": "up",
		})

		// IPv4 unicast
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|ipv4_unicast", neighborIP), ChangeAdd, nil, map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
			"next_hop_self":          "true",
		})

		// IPv6 unicast
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|ipv6_unicast", neighborIP), ChangeAdd, nil, map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
			"next_hop_self":          "true",
		})

		// L2VPN EVPN
		cs.Add("BGP_NEIGHBOR_AF", fmt.Sprintf("default|%s|l2vpn_evpn", neighborIP), ChangeAdd, nil, map[string]string{
			"activate":               "true",
			"route_reflector_client": "true",
		})
	}

	// BGP_GLOBALS_AF for all 3 AFs
	maxPaths := "2"
	if cfg.MaxIBGPPaths > 0 {
		maxPaths = fmt.Sprintf("%d", cfg.MaxIBGPPaths)
	}

	cs.Add("BGP_GLOBALS_AF", "default|ipv4_unicast", ChangeAdd, nil, map[string]string{
		"max_ibgp_paths": maxPaths,
	})
	cs.Add("BGP_GLOBALS_AF", "default|ipv6_unicast", ChangeAdd, nil, map[string]string{
		"max_ibgp_paths": maxPaths,
	})
	cs.Add("BGP_GLOBALS_AF", "default|l2vpn_evpn", ChangeAdd, nil, map[string]string{
		"advertise-all-vni": "true",
	})

	// Route redistribution for connected (loopback + service subnets)
	// Key format: vrf|src_protocol|dst_protocol|addr_family (per SONiC Unified FRR Mgmt HLD)
	cs.Add("ROUTE_REDISTRIBUTE", "default|connected|bgp|ipv4", ChangeAdd, nil, map[string]string{})
	cs.Add("ROUTE_REDISTRIBUTE", "default|connected|bgp|ipv6", ChangeAdd, nil, map[string]string{})

	util.WithDevice(n.Name()).Infof("Setup route reflector with %d neighbors, cluster-id %s",
		len(cfg.Neighbors), clusterID)
	return cs, nil
}

// PeerGroupConfig holds configuration for ConfigurePeerGroup.
type PeerGroupConfig struct {
	Name        string
	ASN         int
	LocalAddr   string
	HoldTime    int
	Keepalive   int
	Password    string
	AdminStatus string
}

// ConfigurePeerGroup creates or updates a BGP peer group template.
func (n *Node) ConfigurePeerGroup(ctx context.Context, cfg PeerGroupConfig) (*ChangeSet, error) {
	if err := n.precondition("configure-peer-group", cfg.Name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.configure-peer-group")

	fields := map[string]string{}
	if cfg.ASN > 0 {
		fields["asn"] = fmt.Sprintf("%d", cfg.ASN)
	}
	if cfg.LocalAddr != "" {
		fields["local_addr"] = cfg.LocalAddr
	}
	if cfg.HoldTime > 0 {
		fields["holdtime"] = fmt.Sprintf("%d", cfg.HoldTime)
	}
	if cfg.Keepalive > 0 {
		fields["keepalive"] = fmt.Sprintf("%d", cfg.Keepalive)
	}
	if cfg.Password != "" {
		fields["password"] = cfg.Password
	}
	adminStatus := cfg.AdminStatus
	if adminStatus == "" {
		adminStatus = "up"
	}
	fields["admin_status"] = adminStatus

	cs.Add("BGP_PEER_GROUP", cfg.Name, ChangeAdd, nil, fields)

	util.WithDevice(n.Name()).Infof("Configured peer group %s", cfg.Name)
	return cs, nil
}

// DeletePeerGroup removes a BGP peer group.
func (n *Node) DeletePeerGroup(ctx context.Context, name string) (*ChangeSet, error) {
	if err := n.precondition("delete-peer-group", name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.delete-peer-group")

	// Delete AF entries first
	configDB := n.ConfigDB()
	if configDB != nil {
		prefix := name + "|"
		for key := range configDB.BGPPeerGroupAF {
			if strings.HasPrefix(key, prefix) {
				cs.Add("BGP_PEER_GROUP_AF", key, ChangeDelete, nil, nil)
			}
		}
	}

	cs.Add("BGP_PEER_GROUP", name, ChangeDelete, nil, nil)

	util.WithDevice(n.Name()).Infof("Deleted peer group %s", name)
	return cs, nil
}

// RouteRedistributionConfig holds configuration for AddRouteRedistribution.
type RouteRedistributionConfig struct {
	VRF           string // VRF name ("default" for global)
	SrcProtocol   string // Source protocol (e.g., "connected", "static")
	AddressFamily string // "ipv4" or "ipv6"
	RouteMap      string // Optional route-map reference
	Metric        string // Optional metric
}

// AddRouteRedistribution configures route redistribution into BGP.
func (n *Node) AddRouteRedistribution(ctx context.Context, cfg RouteRedistributionConfig) (*ChangeSet, error) {
	if err := n.precondition("add-route-redistribution", cfg.SrcProtocol).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.add-route-redistribution")

	vrf := cfg.VRF
	if vrf == "" {
		vrf = "default"
	}

	// Key format: vrf|src_protocol|dst_protocol|addr_family (per SONiC Unified FRR Mgmt HLD)
	key := fmt.Sprintf("%s|%s|bgp|%s", vrf, cfg.SrcProtocol, cfg.AddressFamily)
	fields := map[string]string{}
	if cfg.RouteMap != "" {
		fields["route_map"] = cfg.RouteMap
	}
	if cfg.Metric != "" {
		fields["metric"] = cfg.Metric
	}

	cs.Add("ROUTE_REDISTRIBUTE", key, ChangeAdd, nil, fields)

	util.WithDevice(n.Name()).Infof("Added route redistribution %s %s in VRF %s",
		cfg.SrcProtocol, cfg.AddressFamily, vrf)
	return cs, nil
}

// RemoveRouteRedistribution removes a route redistribution entry.
func (n *Node) RemoveRouteRedistribution(ctx context.Context, vrf, srcProtocol, af string) (*ChangeSet, error) {
	if err := n.precondition("remove-route-redistribution", srcProtocol).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.remove-route-redistribution")

	if vrf == "" {
		vrf = "default"
	}
	// Key format: vrf|src_protocol|dst_protocol|addr_family (per SONiC Unified FRR Mgmt HLD)
	key := fmt.Sprintf("%s|%s|bgp|%s", vrf, srcProtocol, af)
	cs.Add("ROUTE_REDISTRIBUTE", key, ChangeDelete, nil, nil)

	util.WithDevice(n.Name()).Infof("Removed route redistribution %s %s in VRF %s", srcProtocol, af, vrf)
	return cs, nil
}

// RouteMapConfig holds configuration for AddRouteMap.
type RouteMapConfig struct {
	Name           string
	Sequence       int
	Action         string // "permit" or "deny"
	MatchPrefixSet string // Reference to PREFIX_SET
	MatchCommunity string // Reference to COMMUNITY_SET
	MatchASPath    string // Reference to AS_PATH_SET
	SetLocalPref   int
	SetCommunity   string
	SetMED         int
}

// AddRouteMap creates a route-map with match/set rules.
func (n *Node) AddRouteMap(ctx context.Context, cfg RouteMapConfig) (*ChangeSet, error) {
	if err := n.precondition("add-route-map", cfg.Name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.add-route-map")

	key := fmt.Sprintf("%s|%d", cfg.Name, cfg.Sequence)
	fields := map[string]string{
		"route_operation": cfg.Action,
	}
	if cfg.MatchPrefixSet != "" {
		fields["match_prefix_set"] = cfg.MatchPrefixSet
	}
	if cfg.MatchCommunity != "" {
		fields["match_community"] = cfg.MatchCommunity
	}
	if cfg.MatchASPath != "" {
		fields["match_as_path"] = cfg.MatchASPath
	}
	if cfg.SetLocalPref > 0 {
		fields["set_local_pref"] = fmt.Sprintf("%d", cfg.SetLocalPref)
	}
	if cfg.SetCommunity != "" {
		fields["set_community"] = cfg.SetCommunity
	}
	if cfg.SetMED > 0 {
		fields["set_med"] = fmt.Sprintf("%d", cfg.SetMED)
	}

	cs.Add("ROUTE_MAP", key, ChangeAdd, nil, fields)

	util.WithDevice(n.Name()).Infof("Added route-map %s seq %d", cfg.Name, cfg.Sequence)
	return cs, nil
}

// DeleteRouteMap removes a route-map.
func (n *Node) DeleteRouteMap(ctx context.Context, name string) (*ChangeSet, error) {
	if err := n.precondition("delete-route-map", name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.delete-route-map")

	configDB := n.ConfigDB()
	if configDB != nil {
		prefix := name + "|"
		for key := range configDB.RouteMap {
			if strings.HasPrefix(key, prefix) {
				cs.Add("ROUTE_MAP", key, ChangeDelete, nil, nil)
			}
		}
	}

	util.WithDevice(n.Name()).Infof("Deleted route-map %s", name)
	return cs, nil
}

// PrefixSetConfig holds configuration for AddPrefixSet.
type PrefixSetConfig struct {
	Name         string
	Sequence     int
	IPPrefix     string // e.g., "10.0.0.0/8"
	Action       string // "permit" or "deny"
	MaskLenRange string // e.g., "24..32"
}

// AddPrefixSet creates a prefix list for route-map matching.
func (n *Node) AddPrefixSet(ctx context.Context, cfg PrefixSetConfig) (*ChangeSet, error) {
	if err := n.precondition("add-prefix-set", cfg.Name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.add-prefix-set")

	key := fmt.Sprintf("%s|%d", cfg.Name, cfg.Sequence)
	fields := map[string]string{
		"ip_prefix": cfg.IPPrefix,
		"action":    cfg.Action,
	}
	if cfg.MaskLenRange != "" {
		fields["masklength_range"] = cfg.MaskLenRange
	}

	cs.Add("PREFIX_SET", key, ChangeAdd, nil, fields)

	util.WithDevice(n.Name()).Infof("Added prefix-set %s seq %d", cfg.Name, cfg.Sequence)
	return cs, nil
}

// DeletePrefixSet removes a prefix list.
func (n *Node) DeletePrefixSet(ctx context.Context, name string) (*ChangeSet, error) {
	if err := n.precondition("delete-prefix-set", name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.delete-prefix-set")

	configDB := n.ConfigDB()
	if configDB != nil {
		prefix := name + "|"
		for key := range configDB.PrefixSet {
			if strings.HasPrefix(key, prefix) {
				cs.Add("PREFIX_SET", key, ChangeDelete, nil, nil)
			}
		}
	}

	util.WithDevice(n.Name()).Infof("Deleted prefix-set %s", name)
	return cs, nil
}

// AddBGPNetwork adds a BGP network statement.
func (n *Node) AddBGPNetwork(ctx context.Context, vrf, af, prefix string, policy string) (*ChangeSet, error) {
	if err := n.precondition("add-bgp-network", prefix).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.add-bgp-network")

	if vrf == "" {
		vrf = "default"
	}
	key := fmt.Sprintf("%s|%s|%s", vrf, af, prefix)
	fields := map[string]string{}
	if policy != "" {
		fields["policy"] = policy
	}

	cs.Add("BGP_GLOBALS_AF_NETWORK", key, ChangeAdd, nil, fields)

	util.WithDevice(n.Name()).Infof("Added BGP network %s in %s/%s", prefix, vrf, af)
	return cs, nil
}

// RemoveBGPNetwork removes a BGP network statement.
func (n *Node) RemoveBGPNetwork(ctx context.Context, vrf, af, prefix string) (*ChangeSet, error) {
	if err := n.precondition("remove-bgp-network", prefix).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "device.remove-bgp-network")

	if vrf == "" {
		vrf = "default"
	}
	key := fmt.Sprintf("%s|%s|%s", vrf, af, prefix)
	cs.Add("BGP_GLOBALS_AF_NETWORK", key, ChangeDelete, nil, nil)

	util.WithDevice(n.Name()).Infof("Removed BGP network %s from %s/%s", prefix, vrf, af)
	return cs, nil
}
