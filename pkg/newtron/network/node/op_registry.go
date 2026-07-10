package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ============================================================================
// The Operation Registry — the operation plane's schema (Gap 1)
// ============================================================================
//
// Every other plane in the system is table-driven: CONFIG_DB has schema.go,
// parsing has the hydrator registry, the public API has the completeness maps.
// The operation plane was the last holdout — per-op knowledge scattered across
// five hand-synced sites (the intent-params literal in the op body, a
// ReplayStep switch case, an intentParamsToStepParams codec, the ReverseOp
// string, the OperationParams map). This registry is the single owner (§27,
// §39) of the operation-level facts:
//
//   - Scope     — node- or interface-dispatched (drives step-URL form and
//                 replay dispatch)
//   - Inverse   — the §15 reverse verb ("reconcile" for baseline composites,
//                 whose collective reverse is Reconcile)
//   - Params    — the round-trip manifest (§20): which intent params exist,
//                 whether they come from the caller (must round-trip through
//                 steps) or are recorded at apply time for reverse-op
//                 self-sufficiency (re-resolved at replay, never required in
//                 steps), and which are unconditionally present
//   - Replay    — how to re-invoke the operation from a topology step
//   - Export    — how intent params become step params, when the default
//                 pass-through does not fit
//
// TestOpRoundTrip walks the sequence that exercises every entry; its manifest
// assertions check every stored intent against Params. Adding an operation
// without registering it here fails reconstruction loudly ("unknown
// operation") and fails the round-trip coverage guard.

// OpScope says which object an operation is dispatched on during replay.
type OpScope int

const (
	// ScopeNode — replayed as a Node method; step URL "/op".
	ScopeNode OpScope = iota
	// ScopeInterface — replayed as an Interface method; step URL
	// "/interfaces/{name}/op".
	ScopeInterface
)

// ParamSource classifies an intent param for the round-trip manifest.
type ParamSource int

const (
	// SourceCaller — supplied by the caller; the only source at
	// reconstruction time, so it MUST survive intent → step → replay.
	SourceCaller ParamSource = iota
	// SourceRecorded — resolved from specs at apply time and recorded for
	// reverse-op self-sufficiency (§20); re-resolved during replay, so it
	// need not round-trip through steps.
	SourceRecorded
)

// ParamSpec declares one intent param in an operation's manifest.
type ParamSpec struct {
	Key      string
	Source   ParamSource
	Required bool // unconditionally present in every intent this op writes
}

// ReplayFunc re-invokes an operation from step params. i is nil for
// node-scoped operations.
type ReplayFunc func(ctx context.Context, n *Node, i *Interface, p map[string]any) error

// ExportFunc converts a stored intent into step params, for operations whose
// step format diverges from the flat param map (nested fields, key renames,
// CSV → slice). nil means the default pass-through.
type ExportFunc func(intent *sonic.Intent) map[string]any

// OpSpec is one operation's registry entry.
type OpSpec struct {
	Op         string      // wire verb — the operation's URL segment and intent value
	Scope      OpScope     //
	Inverse    string      // §15 reverse verb; "reconcile" for baseline composites
	SideEffect bool        // intent re-created by its parent's replay; never exported
	OpenParams bool        // params include an open field map (setup-device's DEVICE_METADATA)
	Params     []ParamSpec // the round-trip manifest
	Replay     ReplayFunc  // nil iff SideEffect
	Export     ExportFunc  // nil = default pass-through + name re-inject

	// Needs declares the interface capabilities a ScopeInterface forward op
	// requires — checked automatically by n.precondition against the target
	// interface's kind before any op logic runs (§13: prevent, don't
	// detect). nil on a ScopeInterface op means the op's needs depend on
	// request content and it computes them in-method (contentDerivedOps);
	// the registry conformance fence enforces that every ScopeInterface
	// non-SideEffect op is one or the other. Reverse ops are exempt by
	// design: a forward gate guarantees reverses only target legal state,
	// and refusing a reverse would strand intents (§15).
	Needs []InterfaceCapability
}

// contentDerivedOps names the ScopeInterface forward ops whose capability
// needs depend on request content (which service type, routed vs bridged
// config); each computes its needs in-method through
// RequireInterfaceCapabilities on the same checker the automatic gate uses.
var contentDerivedOps = map[string]bool{
	sonic.OpApplyService:       true,
	sonic.OpConfigureInterface: true,
}

// caller/recorded/required are manifest constructors kept short so the
// registry entries below read as tables.
func caller(key string) ParamSpec   { return ParamSpec{Key: key, Source: SourceCaller} }
func required(key string) ParamSpec { return ParamSpec{Key: key, Source: SourceCaller, Required: true} }
func recorded(key string) ParamSpec { return ParamSpec{Key: key, Source: SourceRecorded} }

// opRegistry is the single authoritative table. Keyed by wire verb.
// Populated in init() (not a var literal): the precondition layer reads the
// registry for capability gates, and the Replay closures below reach
// precondition through the op methods — a var literal would be an
// initialization cycle.
var opRegistry map[string]*OpSpec

func init() { opRegistry = buildOpRegistry() }

func buildOpRegistry() map[string]*OpSpec {
	return map[string]*OpSpec{

		// ---------------------------------------------------------------- node ops

		sonic.OpSetupDevice: {
			Op:         sonic.OpSetupDevice,
			Scope:      ScopeNode,
			Inverse:    "reconcile", // baseline composite — §15 exception
			OpenParams: true,        // DEVICE_METADATA fields are an open map
			Params: []ParamSpec{
				caller(sonic.FieldSourceIP),
				caller("rr_cluster_id"), caller("rr_local_asn"), caller("rr_router_id"),
				caller("rr_local_addr"), caller("rr_clients"), caller("rr_peers"),
			},
			Replay: replaySetupDevice,
			Export: exportSetupDevice,
		},

		sonic.OpCreateVRF: {
			Op: sonic.OpCreateVRF, Scope: ScopeNode, Inverse: "device.delete-vrf",
			Params: []ParamSpec{required(sonic.FieldName)},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				name := paramString(p, "name")
				if name == "" {
					return fmt.Errorf("create-vrf: missing 'name' param")
				}
				_, err := n.CreateVRF(ctx, name, VRFConfig{})
				return err
			},
		},

		sonic.OpCreateVLAN: {
			Op: sonic.OpCreateVLAN, Scope: ScopeNode, Inverse: "device.delete-vlan",
			Params: []ParamSpec{
				required(sonic.FieldVLANID), caller(sonic.FieldDescription), caller(sonic.FieldVNI),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				vlanID := paramInt(p, "vlan_id")
				if vlanID == 0 {
					return fmt.Errorf("create-vlan: missing 'vlan_id' param")
				}
				_, err := n.CreateVLAN(ctx, vlanID, VLANConfig{
					Description: paramString(p, "description"),
					L2VNI:       paramInt(p, "vni"),
				})
				return err
			},
		},

		sonic.OpBindMACVPN: {
			Op: sonic.OpBindMACVPN, Scope: ScopeNode, Inverse: "device.unbind-macvpn",
			Params: []ParamSpec{
				required(sonic.FieldVLANID), required(sonic.FieldMACVPN),
				recorded(sonic.FieldVNI), recorded(sonic.FieldARPSuppression),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				vlanID := paramInt(p, "vlan_id")
				macvpnName := paramString(p, "macvpn")
				if vlanID == 0 || macvpnName == "" {
					return fmt.Errorf("bind-macvpn: requires vlan_id and macvpn")
				}
				_, err := n.BindMACVPN(ctx, vlanID, macvpnName)
				return err
			},
		},

		sonic.OpBindIPVPN: {
			Op: sonic.OpBindIPVPN, Scope: ScopeNode, Inverse: "device.unbind-ipvpn",
			Params: []ParamSpec{
				required(sonic.FieldIPVPN),
				recorded(sonic.FieldL3VNI), recorded(sonic.FieldL3VNIVlan), recorded(sonic.FieldRouteTargets),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				// The intent records the IP-VPN spec name in "ipvpn"; BindIPVPN
				// derives the on-device VRF name from it (util.DeriveVRFNameForIPVPN).
				ipvpnName := paramString(p, "ipvpn")
				if ipvpnName == "" {
					return fmt.Errorf("bind-ipvpn: requires 'ipvpn' param")
				}
				_, err := n.BindIPVPN(ctx, ipvpnName)
				return err
			},
		},

		sonic.OpCreatePortChannel: {
			Op: sonic.OpCreatePortChannel, Scope: ScopeNode, Inverse: "device.delete-portchannel",
			Params: []ParamSpec{
				required(sonic.FieldName), caller(sonic.FieldMembers),
				caller("mtu"), caller("min_links"), caller("fallback"), caller("fast_rate"),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				name := paramString(p, "name")
				if name == "" {
					return fmt.Errorf("create-portchannel: missing 'name' param")
				}
				_, err := n.CreatePortChannel(ctx, name, PortChannelConfig{
					Members:  paramStringSlice(p, "members"),
					MTU:      paramInt(p, "mtu"),
					MinLinks: paramInt(p, "min_links"),
					Fallback: paramBool(p, "fallback"),
					FastRate: paramBool(p, "fast_rate"),
				})
				return err
			},
			Export: exportCreatePortChannel,
		},

		sonic.OpAddPortChannelMember: {
			Op: sonic.OpAddPortChannelMember, Scope: ScopeNode, Inverse: "device.remove-portchannel-member",
			Params: []ParamSpec{required(sonic.FieldName), required("portchannel")},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				pcName := paramString(p, "portchannel")
				member := paramString(p, "name")
				if pcName == "" || member == "" {
					return fmt.Errorf("add-pc-member: missing 'portchannel' or 'name' param")
				}
				_, err := n.AddPortChannelMember(ctx, pcName, member)
				return err
			},
		},

		sonic.OpCreateACL: {
			Op: sonic.OpCreateACL, Scope: ScopeNode, Inverse: "device.delete-acl",
			Params: []ParamSpec{
				required(sonic.FieldName), required(sonic.FieldACLType), required(sonic.FieldStage),
				caller(sonic.FieldPorts), caller(sonic.FieldDescription),
				// Service-derived ACLs (content-hashed, written by ApplyService)
				// additionally record their rule set and source filter (§24/§25).
				recorded(sonic.FieldRules), recorded(sonic.FieldFilter),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				name := paramString(p, "name")
				if name == "" {
					return fmt.Errorf("create-acl: missing 'name' param")
				}
				_, err := n.CreateACL(ctx, name, ACLConfig{
					Type:        paramString(p, "type"),
					Stage:       paramString(p, "stage"),
					Ports:       paramString(p, "ports"),
					Description: paramString(p, "description"),
				})
				return err
			},
		},

		sonic.OpAddACLRule: {
			Op: sonic.OpAddACLRule, Scope: ScopeNode, Inverse: "device.remove-acl-rule",
			Params: []ParamSpec{
				required(sonic.FieldName), required("acl"),
				caller("priority"), caller("action"), caller("src_ip"), caller("dst_ip"),
				caller("protocol"), caller("src_port"), caller("dst_port"),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				aclName := paramString(p, "acl")
				ruleName := paramString(p, "name")
				if aclName == "" || ruleName == "" {
					return fmt.Errorf("add-acl-rule: missing 'acl' or 'name' param")
				}
				_, err := n.AddACLRule(ctx, aclName, ruleName, ACLRuleConfig{
					Priority: paramInt(p, "priority"),
					Action:   paramString(p, "action"),
					SrcIP:    paramString(p, "src_ip"),
					DstIP:    paramString(p, "dst_ip"),
					Protocol: paramString(p, "protocol"),
					SrcPort:  paramString(p, "src_port"),
					DstPort:  paramString(p, "dst_port"),
				})
				return err
			},
		},

		sonic.OpConfigureIRB: {
			Op: sonic.OpConfigureIRB, Scope: ScopeNode, Inverse: "device.unconfigure-irb",
			Params: []ParamSpec{
				// The literal stores all four unconditionally (empty strings kept).
				required(sonic.FieldVLANID), ParamSpec{Key: sonic.FieldVRF, Source: SourceCaller, Required: true},
				ParamSpec{Key: sonic.FieldIPAddress, Source: SourceCaller, Required: true},
				ParamSpec{Key: sonic.FieldAnycastMAC, Source: SourceCaller, Required: true},
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				vlanID := paramInt(p, "vlan_id")
				if vlanID == 0 {
					return fmt.Errorf("configure-irb: missing 'vlan_id' param")
				}
				_, err := n.ConfigureIRB(ctx, vlanID, IRBConfig{
					VRF:        paramString(p, "vrf"),
					IPAddress:  paramString(p, "ip_address"),
					AnycastMAC: paramString(p, "anycast_mac"),
				})
				return err
			},
		},

		sonic.OpAddStaticRoute: {
			Op: sonic.OpAddStaticRoute, Scope: ScopeNode, Inverse: "device.remove-static-route",
			Params: []ParamSpec{
				required(sonic.FieldVRF), required(sonic.FieldPrefix), required(sonic.FieldNextHop),
				caller(sonic.FieldMetric),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				vrfName := paramString(p, "vrf")
				prefix := paramString(p, "prefix")
				nextHop := paramString(p, "next_hop")
				metric := paramInt(p, "metric")
				if prefix == "" || nextHop == "" {
					return fmt.Errorf("add-static-route: requires 'prefix' and 'next_hop' params")
				}
				_, err := n.AddStaticRoute(ctx, vrfName, prefix, nextHop, metric)
				return err
			},
		},

		sonic.OpAddBGPEVPNPeer: {
			Op: sonic.OpAddBGPEVPNPeer, Scope: ScopeNode, Inverse: "device.remove-bgp-evpn-peer",
			Params: []ParamSpec{
				required(sonic.FieldNeighborIP), required(sonic.FieldASN),
				ParamSpec{Key: sonic.FieldDescription, Source: SourceCaller, Required: true},
				caller(sonic.FieldEVPN),
			},
			Replay: func(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
				ip := paramString(p, "neighbor_ip")
				asn := paramInt(p, "asn")
				desc := paramString(p, "description")
				evpn := paramBool(p, "evpn")
				if ip == "" || asn == 0 {
					return fmt.Errorf("add-bgp-evpn-peer: requires neighbor_ip and asn")
				}
				_, err := n.AddBGPEVPNPeer(ctx, ip, asn, desc, evpn)
				return err
			},
		},

		// ----------------------------------------------------------- interface ops

		sonic.OpApplyService: {
			Op: sonic.OpApplyService, Scope: ScopeInterface, Inverse: "interface.remove-service",
			Params: []ParamSpec{
				required(sonic.FieldServiceName),
				caller(sonic.FieldIPAddress), caller(sonic.FieldBGPPeerAS), caller(sonic.FieldVLANID),
				caller("route_reflector_client"), caller("next_hop_self"),
				// Recorded for teardown self-sufficiency (§20): RemoveService reads
				// these from the binding, never re-resolving specs.
				ParamSpec{Key: sonic.FieldServiceType, Source: SourceRecorded, Required: true},
				recorded(sonic.FieldVRFName), recorded("vrf_type"),
				recorded(sonic.FieldIPVPN), recorded(sonic.FieldMACVPN),
				recorded("ingress_acl"), recorded("egress_acl"),
				recorded("bgp_neighbor"), recorded("qos_policy"), recorded("peer_group"),
				recorded(sonic.FieldL3VNI), recorded(sonic.FieldL3VNIVlan), recorded(sonic.FieldRouteTargets),
				recorded("redistribute_vrf"), recorded("l2vni"),
				recorded("anycast_ip"), recorded(sonic.FieldAnycastMAC), recorded("arp_suppression"),
			},
			Replay: replayApplyService,
			Export: exportApplyService,
		},

		sonic.OpConfigureInterface: {
			Op: sonic.OpConfigureInterface, Scope: ScopeInterface, Inverse: "interface.unconfigure-interface",
			Params: []ParamSpec{
				caller(sonic.FieldIntfIP), caller(sonic.FieldVLANID),
				caller(sonic.FieldTagged), caller(sonic.FieldVRF),
			},
			Replay: func(ctx context.Context, _ *Node, i *Interface, p map[string]any) error {
				_, err := i.ConfigureInterface(ctx, InterfaceConfig{
					VRF:    paramString(p, "vrf"),
					IP:     paramString(p, "ip"),
					VLAN:   paramInt(p, "vlan_id"),
					Tagged: paramBool(p, "tagged"),
				})
				return err
			},
		},

		sonic.OpAddTrunkVLAN: {
			Op: sonic.OpAddTrunkVLAN, Scope: ScopeInterface, Inverse: "interface." + sonic.OpRemoveTrunkVLAN,
			Needs:  []InterfaceCapability{CapabilityVLANMembership},
			Params: []ParamSpec{required(sonic.FieldVLANID), required(sonic.FieldTagged)},
			Replay: func(ctx context.Context, _ *Node, i *Interface, p map[string]any) error {
				vlanID := paramInt(p, "vlan_id")
				if vlanID == 0 {
					return fmt.Errorf("add-trunk-vlan: missing 'vlan_id' param")
				}
				_, err := i.ConfigureInterface(ctx, InterfaceConfig{VLAN: vlanID, Tagged: true})
				return err
			},
		},

		sonic.OpAddBGPPeer: {
			Op: sonic.OpAddBGPPeer, Scope: ScopeInterface, Inverse: "device.remove-bgp-peer",
			Needs: []InterfaceCapability{CapabilityBGPPeering},
			Params: []ParamSpec{
				required(sonic.FieldNeighborIP), required(sonic.FieldRemoteAS),
				caller(sonic.FieldDescription), caller("multihop"),
			},
			Replay: func(ctx context.Context, _ *Node, i *Interface, p map[string]any) error {
				asn := paramInt(p, "remote_as")
				if asn == 0 {
					return fmt.Errorf("add-bgp-peer: missing 'remote_as' param")
				}
				_, err := i.AddBGPPeer(ctx, DirectBGPPeerConfig{
					NeighborIP:  paramString(p, "neighbor_ip"),
					RemoteAS:    asn,
					Description: paramString(p, "description"),
					Multihop:    paramInt(p, "multihop"),
				})
				return err
			},
		},

		sonic.OpSetProperty: {
			Op: sonic.OpSetProperty, Scope: ScopeInterface, Inverse: "interface.clear-property",
			Needs:  []InterfaceCapability{CapabilityPortProperties},
			Params: []ParamSpec{required(sonic.FieldProperty), required(sonic.FieldValue)},
			Replay: func(ctx context.Context, _ *Node, i *Interface, p map[string]any) error {
				property := paramString(p, "property")
				value := paramString(p, "value")
				if property == "" {
					return fmt.Errorf("set-property: missing 'property' param")
				}
				_, err := i.SetProperty(ctx, property, value)
				return err
			},
		},

		sonic.OpBindACL: {
			Op: sonic.OpBindACL, Scope: ScopeInterface, Inverse: "interface.unbind-acl",
			Needs:  []InterfaceCapability{CapabilityACLBinding},
			Params: []ParamSpec{required(sonic.FieldACLName), required(sonic.FieldDirection)},
			Replay: func(ctx context.Context, _ *Node, i *Interface, p map[string]any) error {
				aclName := paramString(p, "acl_name")
				direction := paramString(p, "direction")
				if aclName == "" {
					return fmt.Errorf("bind-acl: missing 'acl_name' param")
				}
				_, err := i.BindACL(ctx, aclName, direction)
				return err
			},
		},

		sonic.OpBindQoS: {
			Op: sonic.OpBindQoS, Scope: ScopeInterface, Inverse: "interface." + sonic.OpUnbindQoS,
			Needs:  []InterfaceCapability{CapabilityQoSBinding},
			Params: []ParamSpec{required(sonic.FieldQoSPolicy)},
			Replay: func(ctx context.Context, n *Node, i *Interface, p map[string]any) error {
				policyName := paramString(p, "policy")
				if policyName == "" {
					return fmt.Errorf("bind-qos: missing 'policy' param")
				}
				_, err := i.BindQoS(ctx, util.NormalizeName(policyName))
				return err
			},
		},

		// -------------------------------------------------------- side-effect ops
		// Intents written as children of another operation and re-created by that
		// parent's replay. Never exported to steps; no Replay of their own.

		sonic.OpInterfaceInit: {
			Op: sonic.OpInterfaceInit, Scope: ScopeInterface, SideEffect: true,
			// Auto-created by interface sub-resource ops (SetProperty, BindACL,
			// BindQoS, AddBGPPeer); carries no params of its own.
		},

		sonic.OpDeployService: {
			Op: sonic.OpDeployService, Scope: ScopeNode, SideEffect: true,
			// Auto-created by the first ApplyService for a service; records the
			// shared route-policy objects for reference-aware teardown (§24/§25).
			Params: []ParamSpec{
				ParamSpec{Key: sonic.FieldServiceName, Source: SourceRecorded, Required: true},
				recorded("route_map_in"), recorded("route_map_out"), recorded("route_policy_keys"),
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Replay functions too large to inline in the table.
// ---------------------------------------------------------------------------

func replaySetupDevice(ctx context.Context, n *Node, _ *Interface, p map[string]any) error {
	opts := SetupDeviceOpts{
		Fields:   paramStringMap(p, "fields"),
		SourceIP: paramString(p, "source_ip"),
	}
	if rrParams, ok := p["route_reflector"]; ok {
		if rrMap, ok := rrParams.(map[string]any); ok {
			rrOpts, err := parseRouteReflectorOpts(rrMap)
			if err != nil {
				return fmt.Errorf("setup-device route_reflector: %w", err)
			}
			opts.RR = &rrOpts
		}
	}
	_, err := n.SetupDevice(ctx, opts)
	return err
}

func replayApplyService(ctx context.Context, _ *Node, i *Interface, p map[string]any) error {
	serviceName := paramString(p, "service")
	if serviceName == "" {
		return fmt.Errorf("apply-service: missing 'service' param")
	}
	// Normalize service name (topology files may use lowercase with hyphens)
	serviceName = util.NormalizeName(serviceName)
	opts := ApplyServiceOpts{
		IPAddress: paramString(p, "ip_address"),
		PeerAS:    paramInt(p, "peer_as"),
		VLAN:      paramInt(p, "vlan_id"),
	}
	// Topology BGP attributes (route_reflector_client, next_hop_self) flow
	// through Params to ApplyService for correct BGP neighbor configuration.
	if rrc := paramString(p, "route_reflector_client"); rrc != "" {
		if opts.Params == nil {
			opts.Params = make(map[string]string)
		}
		opts.Params["route_reflector_client"] = rrc
	}
	if nhs := paramString(p, "next_hop_self"); nhs != "" {
		if opts.Params == nil {
			opts.Params = make(map[string]string)
		}
		opts.Params["next_hop_self"] = nhs
	}
	_, err := i.ApplyService(ctx, serviceName, opts)
	return err
}

// ---------------------------------------------------------------------------
// Export functions — intent params → step params, where the formats diverge.
// ---------------------------------------------------------------------------

func exportSetupDevice(intent *sonic.Intent) map[string]any {
	// Device metadata fields are nested under "fields" in step format.
	// RR params (rr_*) are reconstructed into a "route_reflector" sub-object.
	result := make(map[string]any)
	fields := make(map[string]any)
	rrParams := make(map[string]any)
	for k, v := range intent.Params {
		switch {
		case k == sonic.FieldSourceIP:
			result[sonic.FieldSourceIP] = v
		case k == "rr_cluster_id":
			rrParams["cluster_id"] = v
		case k == "rr_local_asn":
			rrParams["local_asn"] = v
		case k == "rr_router_id":
			rrParams["router_id"] = v
		case k == "rr_local_addr":
			rrParams["local_addr"] = v
		case k == "rr_clients":
			rrParams["clients"] = deserializeRRPeers(v)
		case k == "rr_peers":
			rrParams["peers"] = deserializeRRPeers(v)
		default:
			fields[k] = v
		}
	}
	if len(fields) > 0 {
		result["fields"] = fields
	}
	if len(rrParams) > 0 {
		result["route_reflector"] = rrParams
	}
	return result
}

func exportApplyService(intent *sonic.Intent) map[string]any {
	// Step format uses "service", intent stores "service_name".
	// Step uses "peer_as", intent stores "bgp_peer_as".
	// Only export caller params needed for replay, not recorded state.
	params := intent.Params
	result := make(map[string]any)
	if v := params[sonic.FieldServiceName]; v != "" {
		result["service"] = v
	}
	if v := params[sonic.FieldIPAddress]; v != "" {
		result[sonic.FieldIPAddress] = v
	}
	if v := params[sonic.FieldBGPPeerAS]; v != "" {
		result["peer_as"] = v
	}
	// VLAN ID for local service types (irb, bridged) where the VLAN
	// comes from opts, not from a macvpn spec.
	if v := params[sonic.FieldVLANID]; v != "" {
		result[sonic.FieldVLANID] = v
	}
	// Topology BGP attributes stored in intent for self-sufficiency.
	// These flow into ApplyServiceOpts.Params for reconstruction.
	if v := params["route_reflector_client"]; v != "" {
		result["route_reflector_client"] = v
	}
	if v := params["next_hop_self"]; v != "" {
		result["next_hop_self"] = v
	}
	return result
}

func exportCreatePortChannel(intent *sonic.Intent) map[string]any {
	// Members are stored as comma-separated string in intent; replay expects []any.
	result := make(map[string]any, len(intent.Params))
	for k, v := range intent.Params {
		if k == sonic.FieldMembers && v != "" {
			parts := strings.Split(v, ",")
			slice := make([]any, len(parts))
			for i, p := range parts {
				slice[i] = p
			}
			result[k] = slice
		} else {
			result[k] = v
		}
	}
	return result
}

// RegisteredOps exposes the registry to conformance sweeps (pkg/conformance)
// and orchestration that dispatches by verb. Callers treat it as read-only —
// the table is assembled once at init and never mutated.
func RegisteredOps() map[string]*OpSpec { return opRegistry }
