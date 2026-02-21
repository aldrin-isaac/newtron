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
// admin-down) are deleted — they're subsumed by the topology-driven config intent
// verification in topology.go's VerifyDeviceHealth.
//
// What remains here: BGP session state (STATE_DB + vtysh fallback) and interface
// oper-status checks. These are called by VerifyDeviceHealth for operational state.

// HealthCheckResult represents the result of a single health check.
type HealthCheckResult struct {
	Check   string `json:"check"`   // Check name (e.g., "bgp", "interface-oper")
	Status  string `json:"status"`  // "pass", "warn", "fail"
	Message string `json:"message"` // Human-readable message
}

// CheckBGPSessions checks that all configured BGP neighbors are Established.
// Reads expected neighbors from CONFIG_DB, then checks STATE_DB (with vtysh fallback).
func (n *Node) CheckBGPSessions(ctx context.Context) ([]HealthCheckResult, error) {
	if !n.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}

	// Start a fresh read-only episode
	if err := n.Refresh(); err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}

	if n.configDB == nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: "Config not loaded"}}, nil
	}

	if len(n.configDB.BGPNeighbor) == 0 {
		return []HealthCheckResult{{Check: "bgp", Status: "warn", Message: "No BGP neighbors configured"}}, nil
	}

	// Build expected neighbor set from CONFIG_DB: vrf → []ip
	expected := make(map[string][]string)
	for key := range n.configDB.BGPNeighbor {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		expected[parts[0]] = append(expected[parts[0]], parts[1])
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

	// Parse vtysh JSON: {"ipv4Unicast": {"peers": {"10.1.0.0": {"state": "Established", ...}}}}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &summary); err != nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: fmt.Sprintf("vtysh parse: %s", err)}}
	}

	// Collect peer states from all address families
	peerStates := make(map[string]string) // ip → state
	for _, afData := range summary {
		var af struct {
			Peers map[string]struct {
				State string `json:"state"`
			} `json:"peers"`
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
	if !n.IsConnected() {
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
