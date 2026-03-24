package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Direct BGP Peer Operations (Interface-level, uses link IP as source)
// ============================================================================
// These operations are for eBGP peers where the BGP session is sourced
// from the interface IP (direct peering over a link).
//
// For iBGP peers using loopback IPs (indirect peering), use the
// device-level BGP operations: Device.AddBGPMultihopPeer() or
// Device.SetupBGPEVPN().

// DirectBGPPeerConfig holds configuration for a direct BGP peer.
type DirectBGPPeerConfig struct {
	NeighborIP  string // Neighbor IP (auto-derived for /30, /31 if empty)
	RemoteAS    int    // Remote AS number (required for eBGP)
	Description string // Optional description
	Password    string // Optional MD5 password
	BFD         bool   // Enable BFD for fast failure detection
	Multihop    int    // eBGP multihop TTL (0 = directly connected)
}

// AddBGPPeer adds a direct BGP peer on this interface.
// The BGP session will use this interface's IP as the update-source.
// For point-to-point links (/30, /31), the neighbor IP is auto-derived if not specified.
func (i *Interface) AddBGPPeer(ctx context.Context, cfg DirectBGPPeerConfig) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("add-bgp-peer", i.name).Result(); err != nil {
		return nil, err
	}
	if cfg.RemoteAS == 0 {
		return nil, fmt.Errorf("remote AS number is required")
	}

	// Interface must have an IP address
	ipAddresses := i.IPAddresses()
	if len(ipAddresses) == 0 {
		return nil, fmt.Errorf("interface %s has no IP address configured", i.name)
	}

	// Get the interface's IP address (use first one)
	localIP := ipAddresses[0]

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

	// Check if BGP peer already exists
	if n.BGPNeighborExists(neighborIP) {
		return nil, fmt.Errorf("BGP peer %s already exists", neighborIP)
	}

	// Extract local IP without mask for update-source
	localIPOnly, _ := util.SplitIPMask(localIP)

	config := CreateBGPNeighborConfig(neighborIP, cfg.RemoteAS, localIPOnly, BGPNeighborOpts{
		Description:  cfg.Description,
		EBGPMultihop: cfg.Multihop > 0,
		MultihopTTL:  fmt.Sprintf("%d", cfg.Multihop),
		ActivateIPv4: true,
	})
	cs := buildChangeSet(n.Name(), "interface.add-bgp-peer", config, ChangeAdd)
	cs.ReverseOp = "device.remove-bgp-peer"
	cs.OperationParams = map[string]string{"neighbor_ip": neighborIP}

	util.WithDevice(n.Name()).Infof("Adding direct BGP peer %s (AS %d) on interface %s",
		neighborIP, cfg.RemoteAS, i.name)
	return cs, nil
}

// RemoveBGPPeer removes a direct BGP peer from this interface.
func (i *Interface) RemoveBGPPeer(ctx context.Context, neighborIP string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("remove-bgp-peer", i.name).Result(); err != nil {
		return nil, err
	}

	// If no neighbor IP specified, try to derive it
	ipAddresses := i.IPAddresses()
	if neighborIP == "" && len(ipAddresses) > 0 {
		var err error
		neighborIP, err = util.DeriveNeighborIP(ipAddresses[0])
		if err != nil {
			return nil, fmt.Errorf("specify neighbor IP to remove")
		}
	}

	return n.RemoveBGPPeer(ctx, neighborIP)
}


