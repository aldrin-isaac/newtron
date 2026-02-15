package network

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Health Checks
// ============================================================================

// HealthCheckResult represents the result of a single health check.
type HealthCheckResult struct {
	Check   string `json:"check"`   // Check name (e.g., "bgp", "interfaces")
	Status  string `json:"status"`  // "pass", "warn", "fail"
	Message string `json:"message"` // Human-readable message
}

// RunHealthChecks runs health checks on the device.
// If checkType is empty, all checks are run.
//
// Starts a fresh read-only episode by calling Refresh() to ensure health
// checks (checkBGP, checkInterfaces, etc.) read current CONFIG_DB state.
func (d *Device) RunHealthChecks(ctx context.Context, checkType string) ([]HealthCheckResult, error) {
	if !d.IsConnected() {
		return nil, util.ErrNotConnected
	}

	// Start a fresh read-only episode
	if err := d.Refresh(); err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}

	var results []HealthCheckResult

	// Run checks based on type
	if checkType == "" || checkType == "bgp" {
		results = append(results, d.checkBGP()...)
	}
	if checkType == "" || checkType == "interfaces" {
		results = append(results, d.checkInterfaces()...)
	}
	if checkType == "" || checkType == "evpn" {
		results = append(results, d.checkEVPN()...)
	}
	if checkType == "" || checkType == "lag" {
		results = append(results, d.checkLAG()...)
	}

	return results, nil
}

func (d *Device) checkBGP() []HealthCheckResult {
	if d.configDB == nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: "Config not loaded"}}
	}

	if len(d.configDB.BGPNeighbor) == 0 {
		return []HealthCheckResult{{Check: "bgp", Status: "warn", Message: "No BGP neighbors configured"}}
	}

	// Build expected neighbor set from CONFIG_DB: vrf → []ip
	expected := make(map[string][]string)
	for key := range d.configDB.BGPNeighbor {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		expected[parts[0]] = append(expected[parts[0]], parts[1])
	}

	// Try STATE_DB first (populated by bgpmon on hardware SONiC)
	if results := d.checkBGPFromStateDB(expected); results != nil {
		return results
	}

	// Fall back to vtysh (VPP and images without bgpmon)
	return d.checkBGPFromVtysh(expected)
}

// checkBGPFromStateDB checks BGP state via STATE_DB BGP_NEIGHBOR_TABLE.
// Returns nil if STATE_DB has no BGP neighbor entries (caller should fall back).
func (d *Device) checkBGPFromStateDB(expected map[string][]string) []HealthCheckResult {
	stateClient := d.conn.StateClient()
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
func (d *Device) checkBGPFromVtysh(expected map[string][]string) []HealthCheckResult {
	tunnel := d.conn.Tunnel()
	if tunnel == nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: "no SSH tunnel for vtysh fallback"}}
	}

	output, err := tunnel.ExecCommand("sudo vtysh -c 'show bgp summary json'")
	if err != nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: fmt.Sprintf("vtysh: %s", err)}}
	}

	// Parse vtysh JSON: {"ipv4Unicast": {"peers": {"10.1.0.0": {"state": "Established", ...}}}}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &summary); err != nil {
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

func (d *Device) checkInterfaces() []HealthCheckResult {
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "interfaces", Status: "fail", Message: "Config not loaded"}}
	}

	total := len(d.configDB.Port)
	adminDown := 0
	for _, port := range d.configDB.Port {
		if port.AdminStatus == "down" {
			adminDown++
		}
	}

	if adminDown > 0 {
		results = append(results, HealthCheckResult{
			Check:   "interfaces",
			Status:  "warn",
			Message: fmt.Sprintf("%d of %d interfaces admin down", adminDown, total),
		})
	} else {
		results = append(results, HealthCheckResult{
			Check:   "interfaces",
			Status:  "pass",
			Message: fmt.Sprintf("All %d interfaces admin up", total),
		})
	}

	return results
}

func (d *Device) checkEVPN() []HealthCheckResult {
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "evpn", Status: "fail", Message: "Config not loaded"}}
	}

	if !d.VTEPExists() {
		results = append(results, HealthCheckResult{
			Check:   "evpn",
			Status:  "warn",
			Message: "No VTEP configured",
		})
	} else {
		vniCount := len(d.configDB.VXLANTunnelMap)
		results = append(results, HealthCheckResult{
			Check:   "evpn",
			Status:  "pass",
			Message: fmt.Sprintf("VTEP configured with %d VNI mappings", vniCount),
		})
	}

	return results
}

func (d *Device) checkLAG() []HealthCheckResult {
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "lag", Status: "fail", Message: "Config not loaded"}}
	}

	lagCount := len(d.configDB.PortChannel)
	if lagCount == 0 {
		results = append(results, HealthCheckResult{
			Check:   "lag",
			Status:  "pass",
			Message: "No LAGs configured",
		})
	} else {
		// Count members
		memberCount := len(d.configDB.PortChannelMember)
		results = append(results, HealthCheckResult{
			Check:   "lag",
			Status:  "pass",
			Message: fmt.Sprintf("%d LAGs configured with %d total members", lagCount, memberCount),
		})
	}

	return results
}
