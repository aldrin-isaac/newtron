package node

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// NormalizeIntentFields returns a copy of an intent record's fields with the
// DAG-link CSVs (_children/_parents) sorted, so two records that differ only in
// link ordering compare equal. This is the canonical form for stable before/
// after intent comparison (IntentSnapshot) and the round-trip equality check.
func NormalizeIntentFields(fields map[string]string) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		if k == "_children" || k == "_parents" {
			if v == "" {
				out[k] = v
				continue
			}
			parts := strings.Split(v, ",")
			sort.Strings(parts)
			v = strings.Join(parts, ",")
		}
		out[k] = v
	}
	return out
}

// IntentSnapshot returns the device's NEWTRON_INTENT records in canonical form
// (every record, DAG-link CSVs sorted) — the substrate for "is the device back
// where it started?" comparisons. Unlike Tree()/IntentsToSteps it does not drop
// side-effect or unreachable records, so a residual/orphaned intent is visible.
//
// In actuated mode it reads the table FRESH from the device (the authority), so
// it catches on-device residual the in-memory projection might not reflect;
// with no transport it falls back to the in-memory intent DB.
func (n *Node) IntentSnapshot(ctx context.Context) (map[string]map[string]string, error) {
	intents := n.configDB.NewtronIntent
	if n.conn != nil {
		if client := n.conn.Client(); client != nil {
			fresh, err := client.GetRawTable("NEWTRON_INTENT")
			if err != nil {
				return nil, fmt.Errorf("reading NEWTRON_INTENT for snapshot: %w", err)
			}
			intents = fresh
		}
	}
	out := make(map[string]map[string]string, len(intents))
	for resource, fields := range intents {
		out[resource] = NormalizeIntentFields(fields)
	}
	return out, nil
}

// ReplayStep dispatches a topology step to the operation registry
// (op_registry.go). The step URL identifies the operation; interface-scoped
// operations include the interface name in the URL
// (e.g., "/interfaces/Ethernet0/apply-service").
//
// Used by the topology provisioner to replay pre-computed steps against an
// abstract Node, and by reconstruct paths to replay intent records.
func ReplayStep(ctx context.Context, n *Node, step spec.TopologyStep) error {
	op, ifaceName := parseStepURL(step.URL)

	opSpec := opRegistry[op]
	if opSpec == nil || opSpec.Replay == nil {
		return fmt.Errorf("unknown operation: %s", op)
	}

	switch opSpec.Scope {
	case ScopeInterface:
		if ifaceName == "" {
			return fmt.Errorf("%s: interface-scoped operation without interface in URL %q", op, step.URL)
		}
		iface, err := n.GetInterface(ifaceName)
		if err != nil {
			return fmt.Errorf("interface %s: %w", ifaceName, err)
		}
		return opSpec.Replay(ctx, n, iface, step.Params)
	default:
		return opSpec.Replay(ctx, n, nil, step.Params)
	}
}

// stepURL constructs a topology step URL from an operation name and optional interface.
// This is the inverse of parseStepURL — both Snapshot and IntentToStep use this encoder.
func stepURL(op, interfaceName string) string {
	if interfaceName != "" {
		return "/interfaces/" + interfaceName + "/" + op
	}
	return "/" + op
}

// parseStepURL extracts the operation name and optional interface name from a step URL.
// Node-level:      "/setup-device"                → op="setup-device", iface=""
// Interface-level: "/interfaces/Ethernet0/apply-service" → op="apply-service", iface="Ethernet0"
func parseStepURL(url string) (op string, ifaceName string) {
	url = strings.TrimPrefix(url, "/")
	parts := strings.Split(url, "/")

	if len(parts) >= 3 && parts[0] == "interfaces" {
		// /interfaces/{name}/{op}
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

// intentInterface returns the interface name for interface-scoped operations,
// or "" for node-scoped ones. Scope comes from the registry: some node-scoped
// operations (configure-irb) use interface| resource keys because the created
// resource IS an interface, but the operation is dispatched at node level.
// Kind-prefixed keys: "interface|Ethernet0", "interface|Ethernet0|acl|ingress".
func intentInterface(op, resource string) string {
	if opSpec := opRegistry[op]; opSpec != nil && opSpec.Scope == ScopeNode {
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

// intentParamsToStepParams converts flat intent fields to structured step
// params. Operations whose step format diverges from the flat map declare an
// Export func in the registry; everything else passes through directly. The
// Intent.Name identity field is re-injected as "name" for operations that
// accept it — NewIntent strips "name" from Params into Intent.Name, and step
// replay expects it back.
func intentParamsToStepParams(op string, intent *sonic.Intent) map[string]any {
	var result map[string]any
	if opSpec := opRegistry[op]; opSpec != nil && opSpec.Export != nil {
		result = opSpec.Export(intent)
	} else {
		result = make(map[string]any, len(intent.Params)+1)
		for k, v := range intent.Params {
			result[k] = v
		}
	}

	if intent.Name != "" {
		if _, exists := result["name"]; !exists {
			result["name"] = intent.Name
		}
	}

	return result
}

// IntentsToSteps converts a map of NEWTRON_INTENT records to an ordered
// slice of topology steps. Steps are ordered by topological sort using
// _parents/_children from the intent DAG (Kahn's algorithm). Ties are
// broken by resource key for determinism.
//
// Side-effect intents (registry SideEffect: interface-init, deploy-service)
// are skipped: replaying their parent operation re-creates them.
func IntentsToSteps(intents map[string]map[string]string) []spec.TopologyStep {
	// Build intent objects, filtering non-actuated and side-effect operations
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
		if opSpec := opRegistry[intent.Operation]; opSpec != nil && opSpec.SideEffect {
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
				continue // child not in node set (side-effect or non-actuated)
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
