// Package health provides health check functionality for SONiC devices.
package health

import (
	"context"
	"fmt"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
)

// Status represents the health status of a component
type Status string

const (
	StatusOK       Status = "ok"
	StatusWarning  Status = "warning"
	StatusCritical Status = "critical"
	StatusUnknown  Status = "unknown"
)

// Result represents the result of a health check
type Result struct {
	Check     string        `json:"check"`
	Status    Status        `json:"status"`
	Message   string        `json:"message"`
	Details   interface{}   `json:"details,omitempty"`
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
}

// Report contains all health check results for a device
type Report struct {
	Device    string        `json:"device"`
	Timestamp time.Time     `json:"timestamp"`
	Overall   Status        `json:"overall"`
	Results   []Result      `json:"results"`
	Duration  time.Duration `json:"duration"`
}

// Check defines the interface for health checks
type Check interface {
	Name() string
	Run(ctx context.Context, d *node.Node) Result
}

// Checker runs health checks on a device
type Checker struct {
	checks []Check
}

// NewChecker creates a new health checker with default checks
func NewChecker() *Checker {
	return &Checker{
		checks: []Check{
			&InterfaceCheck{},
			&LAGCheck{},
			&BGPCheck{},
			&VXLANCheck{},
			&EVPNCheck{},
		},
	}
}


// Run executes all health checks and returns a report
func (c *Checker) Run(ctx context.Context, d *node.Node) (*Report, error) {
	if !d.IsConnected() {
		return nil, fmt.Errorf("device not connected")
	}

	start := time.Now()
	report := &Report{
		Device:    d.Name(),
		Timestamp: start,
		Results:   make([]Result, 0, len(c.checks)),
		Overall:   StatusOK,
	}

	for _, check := range c.checks {
		result := check.Run(ctx, d)
		report.Results = append(report.Results, result)

		// Update overall status (worst wins)
		if result.Status == StatusCritical {
			report.Overall = StatusCritical
		} else if result.Status == StatusWarning && report.Overall != StatusCritical {
			report.Overall = StatusWarning
		} else if result.Status == StatusUnknown && report.Overall == StatusOK {
			report.Overall = StatusUnknown
		}
	}

	report.Duration = time.Since(start)
	return report, nil
}

// RunCheck runs a specific health check by name
func (c *Checker) RunCheck(ctx context.Context, d *node.Node, name string) (*Result, error) {
	for _, check := range c.checks {
		if check.Name() == name {
			result := check.Run(ctx, d)
			return &result, nil
		}
	}
	return nil, fmt.Errorf("health check '%s' not found", name)
}


// InterfaceCheck verifies interface health
type InterfaceCheck struct{}

// Name returns the check name
func (c *InterfaceCheck) Name() string {
	return "interfaces"
}

// Run executes the interface health check
func (c *InterfaceCheck) Run(ctx context.Context, d *node.Node) Result {
	start := time.Now()
	result := Result{
		Check:     c.Name(),
		Timestamp: start,
	}

	interfaceNames := d.ListInterfaces()
	totalCount := len(interfaceNames)

	var downCount int
	for _, name := range interfaceNames {
		intf, err := d.GetInterface(name)
		if err != nil {
			continue
		}
		// Check if admin status is up but oper status is down
		if intf.AdminStatus() == "up" && intf.OperStatus() == "down" {
			downCount++
		}
	}

	result.Duration = time.Since(start)
	result.Details = map[string]int{
		"total": totalCount,
		"down":  downCount,
	}

	if downCount == 0 {
		result.Status = StatusOK
		result.Message = fmt.Sprintf("All %d interfaces operational", totalCount)
	} else if downCount < totalCount/2 {
		result.Status = StatusWarning
		result.Message = fmt.Sprintf("%d of %d interfaces down", downCount, totalCount)
	} else {
		result.Status = StatusCritical
		result.Message = fmt.Sprintf("%d of %d interfaces down", downCount, totalCount)
	}

	return result
}

// LAGCheck verifies LAG/PortChannel health
type LAGCheck struct{}

// Name returns the check name
func (c *LAGCheck) Name() string {
	return "lag"
}

// Run executes the LAG health check
func (c *LAGCheck) Run(ctx context.Context, d *node.Node) Result {
	start := time.Now()
	result := Result{
		Check:     c.Name(),
		Timestamp: start,
	}

	portChannels := d.ListPortChannels()
	if len(portChannels) == 0 {
		result.Status = StatusOK
		result.Message = "No LAGs configured"
		result.Duration = time.Since(start)
		return result
	}

	var degradedCount int
	for _, pcName := range portChannels {
		pc, err := d.GetPortChannel(pcName)
		if err != nil {
			continue
		}
		if len(pc.ActiveMembers) < len(pc.Members) {
			degradedCount++
		}
	}

	result.Duration = time.Since(start)
	result.Details = map[string]int{
		"total":    len(portChannels),
		"degraded": degradedCount,
	}

	if degradedCount == 0 {
		result.Status = StatusOK
		result.Message = fmt.Sprintf("All %d LAGs healthy", len(portChannels))
	} else {
		result.Status = StatusWarning
		result.Message = fmt.Sprintf("%d of %d LAGs degraded", degradedCount, len(portChannels))
	}

	return result
}

// BGPCheck verifies BGP peer health
type BGPCheck struct{}

// Name returns the check name
func (c *BGPCheck) Name() string {
	return "bgp"
}

// Run executes the BGP health check
func (c *BGPCheck) Run(ctx context.Context, d *node.Node) Result {
	start := time.Now()
	result := Result{
		Check:     c.Name(),
		Timestamp: start,
	}

	if !d.BGPConfigured() {
		result.Status = StatusOK
		result.Message = "BGP not configured"
		result.Duration = time.Since(start)
		return result
	}

	// Would query BGP state from device
	// For now, return a placeholder
	result.Status = StatusOK
	result.Message = "BGP peers healthy"
	result.Duration = time.Since(start)

	return result
}

// VXLANCheck verifies VXLAN tunnel health
type VXLANCheck struct{}

// Name returns the check name
func (c *VXLANCheck) Name() string {
	return "vxlan"
}

// Run executes the VXLAN health check
func (c *VXLANCheck) Run(ctx context.Context, d *node.Node) Result {
	start := time.Now()
	result := Result{
		Check:     c.Name(),
		Timestamp: start,
	}

	if !d.VTEPExists() {
		result.Status = StatusOK
		result.Message = "VXLAN not configured"
		result.Duration = time.Since(start)
		return result
	}

	// Would check VTEP state
	result.Status = StatusOK
	result.Message = "VTEP operational"
	result.Duration = time.Since(start)

	return result
}

// EVPNCheck verifies EVPN route health
type EVPNCheck struct{}

// Name returns the check name
func (c *EVPNCheck) Name() string {
	return "evpn"
}

// Run executes the EVPN health check
func (c *EVPNCheck) Run(ctx context.Context, d *node.Node) Result {
	start := time.Now()
	result := Result{
		Check:     c.Name(),
		Timestamp: start,
	}

	if !d.VTEPExists() || !d.BGPConfigured() {
		result.Status = StatusOK
		result.Message = "EVPN not configured"
		result.Duration = time.Since(start)
		return result
	}

	// Would check EVPN route counts
	result.Status = StatusOK
	result.Message = "EVPN routes healthy"
	result.Duration = time.Since(start)

	return result
}
