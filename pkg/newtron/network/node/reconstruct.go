package node

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ReplayStep dispatches a topology step to the appropriate Node or Interface method.
// The step URL identifies the operation; interface-scoped operations include the
// interface name in the URL (e.g., "/interface/Ethernet0/apply-service").
//
// Used by the topology provisioner to replay pre-computed steps against an
// abstract Node, and by reconstruct paths to replay intent records.
func ReplayStep(ctx context.Context, n *Node, step spec.TopologyStep) error {
	op, ifaceName := parseStepURL(step.URL)
	p := step.Params

	// Interface-scoped operations
	if ifaceName != "" {
		iface, err := n.GetInterface(ifaceName)
		if err != nil {
			return fmt.Errorf("interface %s: %w", ifaceName, err)
		}
		return replayInterfaceStep(ctx, iface, op, p)
	}

	// Node-scoped operations
	return replayNodeStep(ctx, n, op, p)
}

// replayNodeStep dispatches a node-level operation.
func replayNodeStep(ctx context.Context, n *Node, op string, p map[string]any) error {
	switch op {
	case "setup-device":
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

	case "add-bgp-evpn-peer":
		ip := paramString(p, "neighbor_ip")
		asn := paramInt(p, "asn")
		desc := paramString(p, "description")
		evpn := paramBool(p, "evpn")
		if ip == "" || asn == 0 {
			return fmt.Errorf("add-bgp-evpn-peer: requires neighbor_ip and asn")
		}
		_, err := n.AddBGPEVPNPeer(ctx, ip, asn, desc, evpn)
		return err

	case "create-vrf":
		name := paramString(p, "name")
		if name == "" {
			return fmt.Errorf("create-vrf: missing 'name' param")
		}
		_, err := n.CreateVRF(ctx, name, VRFConfig{})
		return err

	case "create-vlan":
		vlanID := paramInt(p, "vlan_id")
		if vlanID == 0 {
			return fmt.Errorf("create-vlan: missing 'vlan_id' param")
		}
		_, err := n.CreateVLAN(ctx, vlanID, VLANConfig{
			Description: paramString(p, "description"),
			L2VNI:       paramInt(p, "vni"),
		})
		return err

	case "bind-macvpn":
		vlanID := paramInt(p, "vlan_id")
		macvpnName := paramString(p, "macvpn")
		if vlanID == 0 || macvpnName == "" {
			return fmt.Errorf("bind-macvpn: requires vlan_id and macvpn")
		}
		_, err := n.BindMACVPN(ctx, vlanID, macvpnName)
		return err

	case "create-portchannel":
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

	case "create-acl":
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

	case "configure-irb":
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

	case "bind-ipvpn":
		vrfName := paramString(p, "vrf")
		ipvpnName := paramString(p, "ipvpn")
		if vrfName == "" || ipvpnName == "" {
			return fmt.Errorf("bind-ipvpn: requires 'vrf' and 'ipvpn' params")
		}
		_, err := n.BindIPVPN(ctx, vrfName, util.NormalizeName(ipvpnName))
		return err

	case "add-static-route":
		vrfName := paramString(p, "vrf")
		prefix := paramString(p, "prefix")
		nextHop := paramString(p, "next_hop")
		metric := paramInt(p, "metric")
		if prefix == "" || nextHop == "" {
			return fmt.Errorf("add-static-route: requires 'prefix' and 'next_hop' params")
		}
		_, err := n.AddStaticRoute(ctx, vrfName, prefix, nextHop, metric)
		return err

	default:
		return fmt.Errorf("unknown node operation: %s", op)
	}
}

// replayInterfaceStep dispatches an interface-level operation.
func replayInterfaceStep(ctx context.Context, iface *Interface, op string, p map[string]any) error {
	switch op {
	case "apply-service":
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
		_, err := iface.ApplyService(ctx, serviceName, opts)
		return err

	case "configure-interface":
		_, err := iface.ConfigureInterface(ctx, InterfaceConfig{
			VRF:    paramString(p, "vrf"),
			IP:     paramString(p, "ip"),
			VLAN:   paramInt(p, "vlan_id"),
			Tagged: paramBool(p, "tagged"),
		})
		return err

	case "add-bgp-peer":
		asn := paramInt(p, "remote_as")
		if asn == 0 {
			return fmt.Errorf("add-bgp-peer: missing 'remote_as' param")
		}
		_, err := iface.AddBGPPeer(ctx, DirectBGPPeerConfig{
			NeighborIP:  paramString(p, "neighbor_ip"),
			RemoteAS:    asn,
			Description: paramString(p, "description"),
			Multihop:    paramInt(p, "multihop"),
		})
		return err

	case "set-property":
		property := paramString(p, "property")
		value := paramString(p, "value")
		if property == "" {
			return fmt.Errorf("set-property: missing 'property' param")
		}
		_, err := iface.SetProperty(ctx, property, value)
		return err

	case "bind-acl":
		aclName := paramString(p, "acl_name")
		direction := paramString(p, "direction")
		if aclName == "" {
			return fmt.Errorf("bind-acl: missing 'acl_name' param")
		}
		_, err := iface.BindACL(ctx, aclName, direction)
		return err

	case "apply-qos":
		policyName := paramString(p, "policy")
		if policyName == "" {
			return fmt.Errorf("apply-qos: missing 'policy' param")
		}
		n := iface.Node()
		policy, err := n.GetQoSPolicy(util.NormalizeName(policyName))
		if err != nil {
			return fmt.Errorf("apply-qos: %w", err)
		}
		_, err = iface.ApplyQoS(ctx, util.NormalizeName(policyName), policy)
		return err

	default:
		return fmt.Errorf("unknown interface operation: %s", op)
	}
}

// stepURL constructs a topology step URL from an operation name and optional interface.
// This is the inverse of parseStepURL — both Snapshot and IntentToStep use this encoder.
func stepURL(op, interfaceName string) string {
	if interfaceName != "" {
		return "/interface/" + interfaceName + "/" + op
	}
	return "/" + op
}

// parseStepURL extracts the operation name and optional interface name from a step URL.
// Node-level:      "/setup-device"                → op="setup-device", iface=""
// Interface-level: "/interface/Ethernet0/apply-service" → op="apply-service", iface="Ethernet0"
func parseStepURL(url string) (op string, ifaceName string) {
	url = strings.TrimPrefix(url, "/")
	parts := strings.Split(url, "/")

	if len(parts) >= 3 && parts[0] == "interface" {
		// /interface/{name}/{op}
		return parts[len(parts)-1], parts[1]
	}
	// /op
	return parts[len(parts)-1], ""
}

// ============================================================================
// Intent → Step conversion
// ============================================================================

// IntentToStep converts a flat NEWTRON_INTENT record to a structured topology step.
// This is the inverse of replay: replay reads steps and writes intents,
// IntentToStep reads intents and produces steps.
func IntentToStep(resource string, fields map[string]string) spec.TopologyStep {
	intent := sonic.NewIntent(resource, fields)
	op := intent.Operation

	// Determine interface name from resource key for interface-scoped operations.
	ifaceName := intentInterface(op, resource)

	step := spec.TopologyStep{
		URL:    stepURL(op, ifaceName),
		Params: intentParamsToStepParams(op, intent),
	}
	return step
}

// intentInterface returns the interface name if the operation is interface-scoped,
// or "" for node-scoped operations.
// Kind-prefixed keys: "interface|Ethernet0", "interface|Ethernet0|acl|ingress".
// Some node-scoped operations (create-portchannel, configure-irb) use interface|
// keys because the created resource IS an interface, but the operation itself is
// dispatched at node level.
func intentInterface(op, resource string) string {
	switch op {
	case sonic.OpConfigureIRB, "unconfigure-irb":
		return ""
	}
	if strings.HasPrefix(resource, "interface|") {
		// Strip "interface|" prefix, extract the interface name (first segment)
		rest := resource[len("interface|"):]
		parts := strings.SplitN(rest, "|", 2)
		return parts[0]
	}
	return ""
}

// intentParamsToStepParams converts flat intent fields to structured step params.
// Most params pass through directly. Some need mapping between intent field names
// and step param names (the two serialization formats diverge for certain operations).
// The Intent.Name identity field is re-injected for operations that accept a "name" param.
func intentParamsToStepParams(op string, intent *sonic.Intent) map[string]any {
	params := intent.Params
	result := make(map[string]any, len(params)+1)

	switch op {
	case sonic.OpSetupDevice:
		// Device metadata fields are nested under "fields" in step format.
		// RR params (rr_*) are reconstructed into a "route_reflector" sub-object.
		fields := make(map[string]any)
		rrParams := make(map[string]any)
		for k, v := range params {
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

	case sonic.OpApplyService:
		// Step format uses "service", intent stores "service_name".
		// Step uses "peer_as", intent stores "bgp_peer_as".
		// Only export user-facing params needed for replay, not resolved state.
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

	case sonic.OpCreatePortChannel:
		// Members are stored as comma-separated string in intent; replay expects []any.
		for k, v := range params {
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

	default:
		// Direct pass-through for most operations.
		for k, v := range params {
			result[k] = v
		}
	}

	// Re-inject the Name identity field as "name" param for operations that need it.
	// NewIntent strips "name" from Params into Intent.Name — step replay expects it back.
	if intent.Name != "" {
		if _, exists := result["name"]; !exists {
			result["name"] = intent.Name
		}
	}

	return result
}

// skipInReconstruct lists operations whose intents are re-created as side effects
// of their parent operation during replay. These are skipped in IntentsToSteps
// because replaying the parent re-creates the child intents automatically.
var skipInReconstruct = map[string]bool{
	sonic.OpAddACLRule:           true, // Re-created by ApplyService (filter spec) or CreateACL
	sonic.OpAddPortChannelMember: true, // Re-created by CreatePortChannel (members in opts)
	sonic.OpInterfaceInit:        true, // Auto-created by sub-resource ops (SetProperty, BindACL, ApplyQoS)
}

// IntentsToSteps converts a map of NEWTRON_INTENT records to an ordered
// slice of topology steps. Steps are ordered by topological sort using
// _parents/_children from the intent DAG (Kahn's algorithm). Ties are
// broken by resource key for determinism.
func IntentsToSteps(intents map[string]map[string]string) []spec.TopologyStep {
	// Build intent objects, filtering non-actuated and skip-listed operations
	type node struct {
		resource string
		fields   map[string]string
		intent   *sonic.Intent
	}
	nodes := make(map[string]*node)
	inDegree := make(map[string]int)

	for resource, fields := range intents {
		intent := sonic.NewIntent(resource, fields)
		if !intent.IsActuated() {
			continue
		}
		if skipInReconstruct[intent.Operation] {
			continue
		}
		nodes[resource] = &node{resource, fields, intent}
		inDegree[resource] = 0
	}

	// Count in-degree from parent relationships (only parents that are in the node set)
	for resource, n := range nodes {
		for _, parent := range n.intent.Parents {
			if _, ok := nodes[parent]; ok {
				inDegree[resource]++
			}
		}
	}

	// Kahn's algorithm — BFS from roots (in-degree 0)
	var queue []string
	for resource, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, resource)
		}
	}
	sort.Strings(queue) // deterministic tie-breaking

	var items []node
	for len(queue) > 0 {
		resource := queue[0]
		queue = queue[1:]

		n := nodes[resource]
		items = append(items, *n)

		// Collect children that become ready, sort for determinism
		var ready []string
		for _, child := range n.intent.Children {
			if _, ok := nodes[child]; !ok {
				continue // child not in node set (skipped or non-actuated)
			}
			inDegree[child]--
			if inDegree[child] == 0 {
				ready = append(ready, child)
			}
		}
		sort.Strings(ready)
		queue = append(queue, ready...)
	}

	steps := make([]spec.TopologyStep, 0, len(items))
	for _, item := range items {
		steps = append(steps, IntentToStep(item.resource, item.fields))
	}
	return steps
}

// ============================================================================
// Reconstruction
// ============================================================================

// ReconstructExpected creates an abstract Node from NEWTRON_INTENT records,
// replaying them as topology steps. The resulting Node has the expected
// CONFIG_DB state that should match the actual device if no drift occurred.
func ReconstructExpected(ctx context.Context, sp SpecProvider,
	name string, profile *spec.DeviceProfile,
	resolved *spec.ResolvedProfile,
	intents map[string]map[string]string,
	ports map[string]map[string]string) (*Node, error) {

	n := NewAbstract(sp, name, profile, resolved)
	for portName, fields := range ports {
		n.RegisterPort(portName, fields)
	}

	steps := IntentsToSteps(intents)
	for _, step := range steps {
		if err := ReplayStep(ctx, n, step); err != nil {
			return nil, fmt.Errorf("reconstruct %s: %w", step.URL, err)
		}
	}
	return n, nil
}

// ============================================================================
// Parameter extraction helpers for map[string]any
// ============================================================================

func paramString(p map[string]any, key string) string {
	if p == nil {
		return ""
	}
	v, ok := p[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func paramInt(p map[string]any, key string) int {
	if p == nil {
		return 0
	}
	v, ok := p[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		var i int
		fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}

func paramBool(p map[string]any, key string) bool {
	if p == nil {
		return false
	}
	v, ok := p[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true"
	}
	return false
}

func paramStringMap(p map[string]any, key string) map[string]string {
	if p == nil {
		return nil
	}
	v, ok := p[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, val := range m {
		result[k] = fmt.Sprintf("%v", val)
	}
	return result
}

func paramStringSlice(p map[string]any, key string) []string {
	if p == nil {
		return nil
	}
	v, ok := p[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		result = append(result, fmt.Sprintf("%v", item))
	}
	return result
}

// parseRouteReflectorOpts extracts RouteReflectorOpts from step params.
func parseRouteReflectorOpts(p map[string]any) (RouteReflectorOpts, error) {
	opts := RouteReflectorOpts{
		ClusterID: paramString(p, "cluster_id"),
		LocalASN:  paramInt(p, "local_asn"),
		RouterID:  paramString(p, "router_id"),
		LocalAddr: paramString(p, "local_addr"),
	}

	if clients, ok := p["clients"]; ok {
		if arr, ok := clients.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					opts.Clients = append(opts.Clients, RouteReflectorPeer{
						IP:  paramString(m, "ip"),
						ASN: paramInt(m, "asn"),
					})
				}
			}
		}
	}

	if peers, ok := p["peers"]; ok {
		if arr, ok := peers.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					opts.Peers = append(opts.Peers, RouteReflectorPeer{
						IP:  paramString(m, "ip"),
						ASN: paramInt(m, "asn"),
					})
				}
			}
		}
	}

	return opts, nil
}

// deserializeRRPeers converts a comma-separated "ip:asn" string back into
// the []any format expected by parseRouteReflectorOpts.
func deserializeRRPeers(csv string) []any {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	result := make([]any, 0, len(parts))
	for _, part := range parts {
		idx := strings.LastIndex(part, ":")
		if idx < 0 {
			continue
		}
		result = append(result, map[string]any{
			"ip":  part[:idx],
			"asn": part[idx+1:],
		})
	}
	return result
}
