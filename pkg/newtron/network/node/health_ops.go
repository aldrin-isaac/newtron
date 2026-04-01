package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ============================================================================
// Health Checks — operational state helpers
// ============================================================================
//
// Config-presence checks (checkEVPN, checkPortChannels, checkInterfaces counting
// admin-down) are deleted — they're subsumed by Drift() in the unified pipeline.
//
// What remains here: BGP session state (STATE_DB + vtysh fallback) and interface
// oper-status checks. These are called by HealthCheck for operational state.

// HealthCheckResult represents the result of a single health check.
type HealthCheckResult struct {
	Check   string `json:"check"`   // Check name (e.g., "bgp", "interface-oper")
	Status  string `json:"status"`  // "pass", "warn", "fail"
	Message string `json:"message"` // Human-readable message
}

// CheckBGPSessions checks that all configured BGP neighbors are Established.
// Reads expected neighbors from intent DB (evpn-peer and bgp-peer intents),
// then checks STATE_DB (with vtysh fallback).
// Auto-connects transport if needed.
func (n *Node) CheckBGPSessions(ctx context.Context) ([]HealthCheckResult, error) {
	if n.conn == nil {
		if err := n.ConnectTransport(ctx); err != nil {
			return nil, fmt.Errorf("connecting transport: %w", err)
		}
	}

	// Build expected neighbor set from intents.
	//
	// Three sources of BGP peers:
	// 1. Overlay peers from ConfigureBGPOverlay: derived from device intent (source_ip) +
	//    resolved.BGPNeighbors. ConfigureBGPOverlay creates BGP_NEIGHBOR entries as part of
	//    the device intent's sub-operations — no separate evpn-peer intents.
	// 2. Standalone overlay peers: evpn-peer|{ip} intents from AddBGPEVPNPeer.
	// 3. Underlay peers: interface|{name}|bgp-peer intents from AddBGPPeer.
	expected := make(map[string][]string)
	seenOverlay := make(map[string]bool)

	// Source 1: ConfigureBGPOverlay overlay peers (device intent + resolved profile)
	deviceIntent := n.GetIntent("device")
	if deviceIntent != nil && deviceIntent.Params["source_ip"] != "" && n.resolved != nil {
		for _, peerIP := range n.resolved.BGPNeighbors {
			if peerIP == n.resolved.LoopbackIP {
				continue
			}
			expected["default"] = append(expected["default"], peerIP)
			seenOverlay[peerIP] = true
		}
	}

	// Source 2: Standalone evpn-peer intents (AddBGPEVPNPeer)
	for _, intent := range n.IntentsByPrefix("evpn-peer|") {
		if ip := intent.Params["neighbor_ip"]; ip != "" && !seenOverlay[ip] {
			expected["default"] = append(expected["default"], ip)
			seenOverlay[ip] = true
		}
	}

	// Source 3: Underlay peers (interface|{name}|bgp-peer)
	for resource, intent := range n.IntentsByPrefix("interface|") {
		if strings.Contains(resource, "|bgp-peer") && intent.Params["neighbor_ip"] != "" {
			vrf := "default"
			if v := intent.Params["vrf"]; v != "" {
				vrf = v
			}
			expected[vrf] = append(expected[vrf], intent.Params["neighbor_ip"])
		}
	}

	if len(expected) == 0 {
		return []HealthCheckResult{{Check: "bgp", Status: "warn", Message: "No BGP neighbors configured"}}, nil
	}

	// Try STATE_DB first (populated by bgpmon on hardware SONiC)
	if results := n.checkBGPFromStateDB(expected); results != nil {
		return results, nil
	}

	// Fall back to vtysh (VPP and images without bgpmon)
	return n.checkBGPFromVtysh(expected), nil
}

// checkBGPFromStateDB checks BGP state via STATE_DB BGP_NEIGHBOR_TABLE.
// Returns nil if STATE_DB has no BGP neighbor entries (caller should fall back).
func (n *Node) checkBGPFromStateDB(expected map[string][]string) []HealthCheckResult {
	stateClient := n.conn.StateClient()
	var results []HealthCheckResult
	anyFound := false

	for vrf, neighbors := range expected {
		for _, neighbor := range neighbors {
			entry, err := stateClient.GetBGPNeighborState(vrf, neighbor)
			if err != nil {
				// Entry not found — might be missing bgpmon, defer to fallback
				continue
			}
			anyFound = true
			if entry.State == "Established" {
				results = append(results, HealthCheckResult{
					Check:   "bgp",
					Status:  "pass",
					Message: fmt.Sprintf("BGP neighbor %s (vrf %s): Established", neighbor, vrf),
				})
			} else {
				results = append(results, HealthCheckResult{
					Check:   "bgp",
					Status:  "fail",
					Message: fmt.Sprintf("BGP neighbor %s (vrf %s): %s", neighbor, vrf, entry.State),
				})
			}
		}
	}

	if !anyFound {
		return nil // No STATE_DB entries at all — fall back to vtysh
	}
	return results
}

// checkBGPFromVtysh checks BGP state via "vtysh -c 'show bgp summary json'".
// Used when STATE_DB has no BGP_NEIGHBOR_TABLE entries (e.g., SONiC VPP
// images that don't ship bgpmon).
func (n *Node) checkBGPFromVtysh(expected map[string][]string) []HealthCheckResult {
	tunnel := n.conn.Tunnel()
	if tunnel == nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: "no SSH tunnel for vtysh fallback"}}
	}

	// Use a 30-second timeout for vtysh command execution to prevent indefinite hangs
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, err := tunnel.ExecCommandContext(ctx, "sudo vtysh -c 'show bgp summary json'")
	if err != nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: fmt.Sprintf("vtysh: %s", err)}}
	}

	// Strip null bytes — CiscoVS/Silicon One vtysh occasionally emits \x00 in JSON output.
	cleaned := strings.ReplaceAll(output, "\x00", "")

	// Strip non-JSON prefix (MOTD banners, prompts, sudo output).
	if idx := strings.Index(cleaned, "{"); idx > 0 {
		cleaned = cleaned[idx:]
	}

	// Parse vtysh JSON: {"ipv4Unicast": {"peers": {"10.1.0.0": {"state": "Established", ...}}}}
	// Use json.Decoder to tolerate trailing garbage after the JSON object
	// (CiscoVS vtysh sometimes appends shell prompt or extra braces).
	var summary map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(cleaned))
	if err := decoder.Decode(&summary); err != nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: fmt.Sprintf("vtysh parse: %s", err)}}
	}

	// Collect peer states from all address families.
	//
	// FRR's "show bgp summary json" returns two different formats:
	//   AF-keyed:  {"ipv4Unicast": {"peers": {…}}, "l2VpnEvpn": {"peers": {…}}}
	//   Flat:      {"routerId": "…", "as": 65001, "peers": {…}, …}
	// The flat format appears intermittently (likely a FRR race condition during
	// daemon initialization). Handle both by trying each top-level value as an
	// AF object with nested peers, AND checking for a direct top-level "peers" key.
	type peerInfo struct {
		State string `json:"state"`
	}
	peerStates := make(map[string]string) // ip → state

	// Try flat format: top-level "peers" key contains the peer map directly.
	if peersRaw, ok := summary["peers"]; ok {
		var peers map[string]peerInfo
		if json.Unmarshal(peersRaw, &peers) == nil {
			for ip, peer := range peers {
				peerStates[ip] = peer.State
			}
		}
	}

	// Try AF-keyed format: each top-level key is an address family with nested "peers".
	for _, afData := range summary {
		var af struct {
			Peers map[string]peerInfo `json:"peers"`
		}
		if json.Unmarshal(afData, &af) == nil {
			for ip, peer := range af.Peers {
				peerStates[ip] = peer.State
			}
		}
	}

	var results []HealthCheckResult
	for vrf, neighbors := range expected {
		for _, neighbor := range neighbors {
			state, ok := peerStates[neighbor]
			if !ok {
				results = append(results, HealthCheckResult{
					Check:   "bgp",
					Status:  "fail",
					Message: fmt.Sprintf("BGP neighbor %s (vrf %s): not found in FRR", neighbor, vrf),
				})
			} else if state == "Established" {
				results = append(results, HealthCheckResult{
					Check:   "bgp",
					Status:  "pass",
					Message: fmt.Sprintf("BGP neighbor %s (vrf %s): Established", neighbor, vrf),
				})
			} else {
				results = append(results, HealthCheckResult{
					Check:   "bgp",
					Status:  "fail",
					Message: fmt.Sprintf("BGP neighbor %s (vrf %s): %s", neighbor, vrf, state),
				})
			}
		}
	}

	return results
}

// CheckInterfaceOper reads STATE_DB PORT_TABLE and checks oper_status for
// the specified interfaces. Returns one HealthCheckResult per interface.
func (n *Node) CheckInterfaceOper(interfaces []string) []HealthCheckResult {
	if n.conn == nil {
		return []HealthCheckResult{{Check: "interface-oper", Status: "fail", Message: "device not connected"}}
	}

	stateClient := n.conn.StateClient()
	if stateClient == nil {
		return []HealthCheckResult{{Check: "interface-oper", Status: "fail", Message: "STATE_DB client not connected"}}
	}

	var results []HealthCheckResult
	for _, intf := range interfaces {
		entry, err := stateClient.GetEntry("PORT_TABLE", intf)
		if err != nil {
			results = append(results, HealthCheckResult{
				Check:   "interface-oper",
				Status:  "fail",
				Message: fmt.Sprintf("%s: STATE_DB read error: %s", intf, err),
			})
			continue
		}
		if entry == nil {
			results = append(results, HealthCheckResult{
				Check:   "interface-oper",
				Status:  "warn",
				Message: fmt.Sprintf("%s: not found in STATE_DB PORT_TABLE", intf),
			})
			continue
		}
		operStatus := entry["oper_status"]
		if operStatus == "" {
			operStatus = entry["netdev_oper_status"]
		}
		if operStatus == "up" {
			results = append(results, HealthCheckResult{
				Check:   "interface-oper",
				Status:  "pass",
				Message: fmt.Sprintf("%s: oper-up", intf),
			})
		} else {
			results = append(results, HealthCheckResult{
				Check:   "interface-oper",
				Status:  "fail",
				Message: fmt.Sprintf("%s: oper-%s", intf, operStatus),
			})
		}
	}

	return results
}
