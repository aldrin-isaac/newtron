package network

import (
	"context"
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
	var results []HealthCheckResult

	if d.configDB == nil {
		return []HealthCheckResult{{Check: "bgp", Status: "fail", Message: "Config not loaded"}}
	}

	if len(d.configDB.BGPNeighbor) == 0 {
		return []HealthCheckResult{{Check: "bgp", Status: "warn", Message: "No BGP neighbors configured"}}
	}

	stateClient := d.conn.StateClient()
	for key := range d.configDB.BGPNeighbor {
		// Key format: "vrf|ip" (e.g., "default|10.1.0.1")
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			results = append(results, HealthCheckResult{
				Check:   "bgp",
				Status:  "fail",
				Message: fmt.Sprintf("Malformed BGP neighbor key: %s", key),
			})
			continue
		}
		vrf, neighbor := parts[0], parts[1]

		entry, err := stateClient.GetBGPNeighborState(vrf, neighbor)
		if err != nil {
			results = append(results, HealthCheckResult{
				Check:   "bgp",
				Status:  "fail",
				Message: fmt.Sprintf("BGP neighbor %s (vrf %s): not found in STATE_DB", neighbor, vrf),
			})
			continue
		}

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
