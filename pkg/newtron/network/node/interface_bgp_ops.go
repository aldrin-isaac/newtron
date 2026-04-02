package node

import (
	"context"
	"fmt"
	"strconv"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Direct BGP Peer Operations (Interface-level, uses link IP as source)
// ============================================================================
// These operations are for eBGP peers where the BGP session is sourced
// from the interface IP (direct peering over a link).
//
// For iBGP peers using loopback IPs (indirect peering), use the
// device-level BGP operations: Device.AddBGPEVPNPeer() or
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

	if err := n.precondition(sonic.OpAddBGPPeer, i.name).Result(); err != nil {
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
		VRF:          i.VRF(),
		Description:  cfg.Description,
		EBGPMultihop: cfg.Multihop > 0,
		MultihopTTL:  fmt.Sprintf("%d", cfg.Multihop),
		ActivateIPv4: true,
	})
	cs := buildChangeSet(n.Name(), "interface."+sonic.OpAddBGPPeer, config, ChangeAdd)
	if err := i.ensureInterfaceIntent(cs); err != nil {
		return nil, err
	}
	intentParams := map[string]string{
		sonic.FieldNeighborIP: neighborIP,
		sonic.FieldRemoteAS:   strconv.Itoa(cfg.RemoteAS),
	}
	if cfg.Description != "" {
		intentParams[sonic.FieldDescription] = cfg.Description
	}
	if cfg.Multihop > 0 {
		intentParams["multihop"] = strconv.Itoa(cfg.Multihop)
	}
	if err := n.writeIntent(cs, sonic.OpAddBGPPeer, "interface|"+i.name+"|bgp-peer", intentParams, []string{"interface|" + i.name}); err != nil {
		return nil, err
	}
	cs.ReverseOp = "device.remove-bgp-peer"
	cs.OperationParams = map[string]string{"interface": i.name, "neighbor_ip": neighborIP}

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Adding direct BGP peer %s (AS %d) on interface %s",
		neighborIP, cfg.RemoteAS, i.name)
	return cs, nil
}

// RemoveBGPPeer removes a direct BGP peer from this interface.
// The neighbor IP is read from the intent record — callers do not need to specify it.
func (i *Interface) RemoveBGPPeer(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("remove-bgp-peer", i.name).Result(); err != nil {
		return nil, err
	}

	// Read neighbor IP from intent record (sub-resource key)
	intentKey := "interface|" + i.name + "|bgp-peer"
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no BGP peer intent for %s", i.name)
	}
	neighborIP := intent.Params[sonic.FieldNeighborIP]
	if neighborIP == "" {
		return nil, fmt.Errorf("intent for %s has no neighbor IP", i.name)
	}

	// Use the interface's VRF for the BGP_NEIGHBOR key (matches the add path).
	vrf := i.VRF()
	config := DeleteBGPNeighborConfig(vrf, neighborIP)
	cs := buildChangeSet(n.Name(), "interface.remove-bgp-peer", config, ChangeDelete)
	if err := n.render(cs); err != nil {
		return nil, err
	}
	// Delete the bgp-peer sub-resource intent. The parent interface|<name>
	// intent is preserved — it belongs to ConfigureInterface.
	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Removing direct BGP peer %s from interface %s", neighborIP, i.name)
	return cs, nil
}


