package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Device Setup (consolidated baseline operation)
// ============================================================================

// SetupDeviceOpts holds the configuration for SetupDevice.
type SetupDeviceOpts struct {
	Fields   map[string]string  // device metadata fields (hostname, bgp_asn, etc.)
	SourceIP string             // VTEP source IP (optional; empty = skip VTEP setup)
	RR       *RouteReflectorOpts // route reflector config (optional; nil = skip)
}

// SetupDevice is the consolidated baseline operation that initializes a device
// for the fabric. It performs: metadata, loopback, BGP, VTEP (optional), and
// route reflector (optional). One intent record for the whole composite.
//
// The sub-operations (SetDeviceMetadata, ConfigureLoopback, ConfigureBGP,
// SetupVTEP, ConfigureRouteReflector) remain available as individual methods
// but do NOT write intent records — SetupDevice is the intent-producing entry point.
func (n *Node) SetupDevice(ctx context.Context, opts SetupDeviceOpts) (*ChangeSet, error) {
	if err := n.precondition(sonic.OpSetupDevice, n.name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device."+sonic.OpSetupDevice)

	// Intent record — captures the input params for reconstruction.
	intentParams := make(map[string]string)
	for k, v := range opts.Fields {
		intentParams[k] = v
	}
	if opts.SourceIP != "" {
		intentParams[sonic.FieldSourceIP] = opts.SourceIP
	}
	if opts.RR != nil {
		serializeRROpts(intentParams, opts.RR)
	}
	if err := n.writeIntent(cs, sonic.OpSetupDevice, "device", intentParams, nil); err != nil {
		return nil, err
	}

	// 1. Device metadata
	if len(opts.Fields) > 0 {
		mdCS, err := n.SetDeviceMetadata(ctx, opts.Fields)
		if err != nil {
			return nil, fmt.Errorf("set-device-metadata: %w", err)
		}
		cs.Merge(mdCS)
	}

	// 2. Loopback
	lbCS, err := n.ConfigureLoopback(ctx)
	if err != nil {
		return nil, fmt.Errorf("configure-loopback: %w", err)
	}
	cs.Merge(lbCS)

	// 3. BGP
	bgpCS, err := n.ConfigureBGP(ctx)
	if err != nil {
		return nil, fmt.Errorf("configure-bgp: %w", err)
	}
	cs.Merge(bgpCS)

	// 4. VTEP (optional — skip if no source IP and no resolved VTEP IP)
	if opts.SourceIP != "" || (n.resolved != nil && n.resolved.VTEPSourceIP != "") {
		vtepCS, err := n.SetupVTEP(ctx, opts.SourceIP)
		if err != nil {
			return nil, fmt.Errorf("setup-vtep: %w", err)
		}
		cs.Merge(vtepCS)
	}

	// 5. Route reflector (optional)
	if opts.RR != nil {
		rrCS, err := n.ConfigureRouteReflector(ctx, *opts.RR)
		if err != nil {
			return nil, fmt.Errorf("configure-route-reflector: %w", err)
		}
		cs.Merge(rrCS)
	}

	util.WithDevice(n.name).Infof("Device setup complete")
	return cs, nil
}

// serializeRROpts flattens RouteReflectorOpts into intent params.
// Peers are stored as comma-separated "ip:asn" pairs.
func serializeRROpts(params map[string]string, rr *RouteReflectorOpts) {
	if rr.ClusterID != "" {
		params["rr_cluster_id"] = rr.ClusterID
	}
	if rr.LocalASN > 0 {
		params["rr_local_asn"] = fmt.Sprintf("%d", rr.LocalASN)
	}
	if rr.RouterID != "" {
		params["rr_router_id"] = rr.RouterID
	}
	if rr.LocalAddr != "" {
		params["rr_local_addr"] = rr.LocalAddr
	}
	if len(rr.Clients) > 0 {
		parts := make([]string, len(rr.Clients))
		for i, c := range rr.Clients {
			parts[i] = fmt.Sprintf("%s:%d", c.IP, c.ASN)
		}
		params["rr_clients"] = strings.Join(parts, ",")
	}
	if len(rr.Peers) > 0 {
		parts := make([]string, len(rr.Peers))
		for i, p := range rr.Peers {
			parts[i] = fmt.Sprintf("%s:%d", p.IP, p.ASN)
		}
		params["rr_peers"] = strings.Join(parts, ",")
	}
}

// ============================================================================
// Loopback Configuration
// ============================================================================

// ConfigureLoopback creates the Loopback0 interface with the device's loopback IP.
// Reads the IP from the resolved profile — no vars indirection needed.
func (n *Node) ConfigureLoopback(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("configure-loopback", "loopback").Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.configure-loopback")
	cs.ReverseOp = "device.remove-loopback"

	loopbackIP := ""
	if n.resolved != nil {
		loopbackIP = n.resolved.LoopbackIP
	}
	if loopbackIP == "" {
		return nil, fmt.Errorf("no loopback IP configured for device %s", n.name)
	}

	// Base entry required for intfmgrd to bind the IP (Update = idempotent create-or-update)
	cs.Update("LOOPBACK_INTERFACE", "Loopback0", map[string]string{})
	cs.Add("LOOPBACK_INTERFACE", fmt.Sprintf("Loopback0|%s/32", loopbackIP), map[string]string{})

	n.applyShadow(cs)
	util.WithDevice(n.name).Infof("Configured Loopback0 with IP %s/32", loopbackIP)
	return cs, nil
}

// RemoveLoopback removes all Loopback0 entries from CONFIG_DB.
// Reverses ConfigureLoopback: deletes base entry and all IP sub-entries.
func (n *Node) RemoveLoopback(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("remove-loopback", "loopback").Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.remove-loopback")

	configDB := n.ConfigDB()
	if configDB == nil {
		return cs, nil
	}

	// Delete all LOOPBACK_INTERFACE entries for Loopback0 (IP sub-entries first, then base)
	for key := range configDB.LoopbackInterface {
		if key == "Loopback0" || strings.HasPrefix(key, "Loopback0|") {
			cs.Delete("LOOPBACK_INTERFACE", key)
		}
	}

	util.WithDevice(n.name).Infof("Removed Loopback0 configuration")
	return cs, nil
}
