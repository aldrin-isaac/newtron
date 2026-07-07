package newtron

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
)

// ============================================================================
// Public → internal boundary conversions (§33: every type that crosses the
// boundary gets a conversion function, even when the treatment is trivial
// today — the seam must already exist when simple cases become complex).
//
// Where the public and internal types are field-identical, the body is a
// direct Go struct conversion: it stops compiling the moment either side
// gains a field, so the pair cannot diverge silently — the error lands
// here, in the one place that owns the crossing, and no call site moves.
// Where the vocabularies genuinely differ, the body is the one hand-written
// field mapping for that pair.
// ============================================================================

func (c VLANConfig) internal() node.VLANConfig { return node.VLANConfig(c) }

func (c IRBConfig) internal() node.IRBConfig { return node.IRBConfig(c) }

func (c VRFConfig) internal() node.VRFConfig { return node.VRFConfig(c) }

func (c ACLConfig) internal() node.ACLConfig { return node.ACLConfig(c) }

func (c ACLRuleConfig) internal() node.ACLRuleConfig { return node.ACLRuleConfig(c) }

func (c PortChannelConfig) internal() node.PortChannelConfig { return node.PortChannelConfig(c) }

func (c InterfaceConfig) internal() node.InterfaceConfig { return node.InterfaceConfig(c) }

func (o ReconcileOpts) internal() node.ReconcileOpts { return node.ReconcileOpts(o) }

func (o ApplyServiceOpts) internal() node.ApplyServiceOpts { return node.ApplyServiceOpts(o) }

// SetupDeviceOpts nests RouteReflectorOpts, whose slice element type is
// package-local on both sides — a direct conversion is illegal, so this
// pair is hand-mapped.
func (o SetupDeviceOpts) internal() node.SetupDeviceOpts {
	out := node.SetupDeviceOpts{
		Fields:   o.Fields,
		SourceIP: o.SourceIP,
	}
	if o.RR != nil {
		rr := o.RR.internal()
		out.RR = &rr
	}
	return out
}

func (o RouteReflectorOpts) internal() node.RouteReflectorOpts {
	out := node.RouteReflectorOpts{
		ClusterID: o.ClusterID,
		LocalASN:  o.LocalASN,
		RouterID:  o.RouterID,
		LocalAddr: o.LocalAddr,
	}
	for _, c := range o.Clients {
		out.Clients = append(out.Clients, node.RouteReflectorPeer{IP: c.IP, ASN: c.ASN})
	}
	for _, p := range o.Peers {
		out.Peers = append(out.Peers, node.RouteReflectorPeer{IP: p.IP, ASN: p.ASN})
	}
	return out
}

// directPeer maps the public BGP neighbor vocabulary to the interface-scoped
// direct-peer type. Password and BFD exist internally with no public surface
// and no registry-manifest entry — dormant capability, deliberately unmapped
// until an operation declares them (the manifest is the wire-completeness
// yardstick).
func (c BGPNeighborConfig) directPeer() node.DirectBGPPeerConfig {
	return node.DirectBGPPeerConfig{
		NeighborIP:  c.NeighborIP,
		RemoteAS:    c.RemoteAS,
		Description: c.Description,
		Multihop:    c.Multihop,
	}
}
