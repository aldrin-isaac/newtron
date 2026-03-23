package node

import (
	"context"
	"fmt"
	"strings"

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
	case "configure-loopback":
		_, err := n.ConfigureLoopback(ctx)
		return err

	case "set-device-metadata":
		fields := paramStringMap(p, "fields")
		if len(fields) == 0 {
			return fmt.Errorf("set-device-metadata: missing 'fields' param")
		}
		_, err := n.SetDeviceMetadata(ctx, fields)
		return err

	case "configure-bgp":
		_, err := n.ConfigureBGP(ctx)
		return err

	case "setup-vtep":
		sourceIP := paramString(p, "source_ip")
		_, err := n.SetupVTEP(ctx, sourceIP)
		return err

	case "add-overlay-peer":
		ip := paramString(p, "neighbor_ip")
		asn := paramInt(p, "asn")
		desc := paramString(p, "description")
		evpn := paramBool(p, "evpn")
		if ip == "" || asn == 0 {
			return fmt.Errorf("add-overlay-peer: requires neighbor_ip and asn")
		}
		_, err := n.AddOverlayPeer(ctx, ip, asn, desc, evpn)
		return err

	case "configure-route-reflector":
		opts, err := parseRouteReflectorOpts(p)
		if err != nil {
			return fmt.Errorf("configure-route-reflector: %w", err)
		}
		_, err = n.ConfigureRouteReflector(ctx, opts)
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
		_, err := n.CreateVLAN(ctx, vlanID, VLANConfig{})
		return err

	case "map-l2vni":
		vlanID := paramInt(p, "vlan_id")
		vni := paramInt(p, "vni")
		if vlanID == 0 || vni == 0 {
			return fmt.Errorf("map-l2vni: requires vlan_id and vni")
		}
		_, err := n.MapL2VNI(ctx, vlanID, vni)
		return err

	case "create-portchannel":
		name := paramString(p, "name")
		if name == "" {
			return fmt.Errorf("create-portchannel: missing 'name' param")
		}
		members := paramStringSlice(p, "members")
		_, err := n.CreatePortChannel(ctx, name, PortChannelConfig{
			Members: members,
		})
		return err

	case "create-acl-table":
		name := paramString(p, "name")
		if name == "" {
			return fmt.Errorf("create-acl-table: missing 'name' param")
		}
		_, err := n.CreateACLTable(ctx, name, ACLTableConfig{
			Type:  paramString(p, "type"),
			Stage: paramString(p, "stage"),
			Ports: paramString(p, "ports"),
		})
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
		_, err := iface.ApplyService(ctx, serviceName, ApplyServiceOpts{
			IPAddress: paramString(p, "ip_address"),
			PeerAS:    paramInt(p, "peer_as"),
		})
		return err

	case "configure-interface":
		_, err := iface.ConfigureInterface(ctx, InterfaceConfig{
			VRF: paramString(p, "vrf"),
			IP:  paramString(p, "ip"),
		})
		return err

	case "add-bgp-neighbor":
		asn := paramInt(p, "remote_as")
		if asn == 0 {
			return fmt.Errorf("add-bgp-neighbor: missing 'remote_as' param")
		}
		_, err := iface.AddBGPNeighbor(ctx, DirectBGPNeighborConfig{
			NeighborIP:  paramString(p, "neighbor_ip"),
			RemoteAS:    asn,
			Description: paramString(p, "description"),
		})
		return err

	default:
		return fmt.Errorf("unknown interface operation: %s", op)
	}
}

// parseStepURL extracts the operation name and optional interface name from a step URL.
// Node-level:      "/configure-bgp"              → op="configure-bgp", iface=""
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
