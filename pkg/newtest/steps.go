package newtest

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/network"
)

// StepExecutor executes a single step and returns output.
type StepExecutor interface {
	Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
}

// StepOutput is the return value from every executor.
type StepOutput struct {
	Result     *StepResult
	ChangeSets map[string]*network.ChangeSet
}

// executors maps each StepAction to its executor implementation.
var executors = map[StepAction]StepExecutor{
	ActionProvision:          &provisionExecutor{},
	ActionWait:               &waitExecutor{},
	ActionVerifyProvisioning: &verifyProvisioningExecutor{},
	ActionVerifyConfigDB:     &verifyConfigDBExecutor{},
	ActionVerifyStateDB:      &verifyStateDBExecutor{},
	ActionVerifyBGP:          &verifyBGPExecutor{},
	ActionVerifyHealth:       &verifyHealthExecutor{},
	ActionVerifyRoute:        &verifyRouteExecutor{},
	ActionVerifyPing:         &verifyPingExecutor{},
	ActionApplyService:       &applyServiceExecutor{},
	ActionRemoveService:      &removeServiceExecutor{},
	ActionApplyBaseline:      &applyBaselineExecutor{},
	ActionSSHCommand:         &sshCommandExecutor{},
	ActionRestartService:     &restartServiceExecutor{},
	ActionApplyFRRDefaults:   &applyFRRDefaultsExecutor{},
}

// ============================================================================
// provisionExecutor
// ============================================================================

type provisionExecutor struct{}

func (e *provisionExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)

	provisioner, err := network.NewTopologyProvisioner(r.Network)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Message: fmt.Sprintf("creating provisioner: %s", err),
		}}
	}

	var details []DeviceResult
	allPassed := true

	for _, name := range devices {
		// Generate composite config offline, then deliver using the shared
		// connection (without disconnecting). ProvisionDevice() can't be used
		// here because it calls defer dev.Disconnect() which kills the shared
		// test runner connection.
		composite, err := provisioner.GenerateDeviceComposite(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("generate composite: %s", err),
			})
			allPassed = false
			continue
		}

		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("get device: %s", err),
			})
			allPassed = false
			continue
		}

		if err := dev.Lock(); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("lock: %s", err),
			})
			allPassed = false
			continue
		}

		result, err := dev.DeliverComposite(composite, network.CompositeOverwrite)
		dev.Unlock()
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("deliver composite: %s", err),
			})
			allPassed = false
			continue
		}

		// Refresh the device's cached CONFIG_DB and interface list so
		// subsequent steps see the newly provisioned PORT entries.
		if err := dev.Refresh(); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("refresh after provision: %s", err),
			})
			allPassed = false
			continue
		}

		details = append(details, DeviceResult{
			Device: name, Status: StatusPassed,
			Message: fmt.Sprintf("provisioned (%d entries applied)", result.Applied),
		})
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{
		Result: &StepResult{Status: status, Details: details},
	}
}

// ============================================================================
// waitExecutor
// ============================================================================

type waitExecutor struct{}

func (e *waitExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	select {
	case <-time.After(step.Duration):
	case <-ctx.Done():
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Message: "interrupted",
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status:  StatusPassed,
		Message: fmt.Sprintf("%s elapsed", step.Duration),
	}}
}

// ============================================================================
// verifyProvisioningExecutor
// ============================================================================

type verifyProvisioningExecutor struct{}

func (e *verifyProvisioningExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult

	allPassed := true
	for _, name := range devices {
		cs, ok := r.ChangeSets[name]
		if !ok {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: "no ChangeSet accumulated (was provision run first?)",
			})
			allPassed = false
			continue
		}

		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		if err := cs.Verify(dev); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("verification error: %s", err),
			})
			allPassed = false
			continue
		}

		v := cs.Verification
		total := v.Passed + v.Failed
		if v.Failed == 0 {
			details = append(details, DeviceResult{
				Device: name, Status: StatusPassed,
				Message: fmt.Sprintf("%d/%d CONFIG_DB entries verified", v.Passed, total),
			})
		} else {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("%d/%d CONFIG_DB entries verified (%d failed)", v.Passed, total, v.Failed),
			})
			allPassed = false
		}
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// ============================================================================
// verifyConfigDBExecutor
// ============================================================================

type verifyConfigDBExecutor struct{}

func (e *verifyConfigDBExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		underlying := dev.Underlying()
		if underlying.ConfigDB == nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: "CONFIG_DB not loaded",
			})
			allPassed = false
			continue
		}

		result := e.checkDevice(underlying, step)
		details = append(details, DeviceResult{
			Device: name, Status: result.status, Message: result.message,
		})
		if result.status != StatusPassed {
			allPassed = false
		}
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

type checkResult struct {
	status  Status
	message string
}

func (e *verifyConfigDBExecutor) checkDevice(d *device.Device, step *Step) checkResult {
	client := d.Client()
	if client == nil {
		return checkResult{StatusError, "no CONFIG_DB client"}
	}

	// Mode 1: min_entries
	if step.Expect.MinEntries != nil {
		var count int
		if step.Key == "" {
			// No key: count all entries in the table
			keys, err := client.TableKeys(step.Table)
			if err != nil {
				return checkResult{StatusError, fmt.Sprintf("scanning %s: %s", step.Table, err)}
			}
			count = len(keys)
		} else {
			// Specific key: count fields in that entry
			vals, err := client.Get(step.Table, step.Key)
			if err != nil {
				return checkResult{StatusError, fmt.Sprintf("reading %s|%s: %s", step.Table, step.Key, err)}
			}
			count = len(vals)
		}
		if count >= *step.Expect.MinEntries {
			return checkResult{StatusPassed, fmt.Sprintf("%s: %d entries (≥ %d)", step.Table, count, *step.Expect.MinEntries)}
		}
		return checkResult{StatusFailed, fmt.Sprintf("%s: %d entries (expected ≥ %d)", step.Table, count, *step.Expect.MinEntries)}
	}

	// Mode 2: exists
	if step.Expect.Exists != nil {
		exists, err := client.Exists(step.Table, step.Key)
		if err != nil {
			return checkResult{StatusError, fmt.Sprintf("checking %s|%s: %s", step.Table, step.Key, err)}
		}
		if exists == *step.Expect.Exists {
			return checkResult{StatusPassed, fmt.Sprintf("%s|%s: exists=%v", step.Table, step.Key, exists)}
		}
		return checkResult{StatusFailed, fmt.Sprintf("%s|%s: expected exists=%v, got %v", step.Table, step.Key, *step.Expect.Exists, exists)}
	}

	// Mode 3: fields
	if len(step.Expect.Fields) > 0 {
		vals, err := client.Get(step.Table, step.Key)
		if err != nil {
			return checkResult{StatusError, fmt.Sprintf("reading %s|%s: %s", step.Table, step.Key, err)}
		}
		if len(vals) == 0 {
			return checkResult{StatusFailed, fmt.Sprintf("%s|%s: not found", step.Table, step.Key)}
		}
		for field, expected := range step.Expect.Fields {
			actual, ok := vals[field]
			if !ok {
				return checkResult{StatusFailed, fmt.Sprintf("%s|%s: field %s missing", step.Table, step.Key, field)}
			}
			if actual != expected {
				return checkResult{StatusFailed, fmt.Sprintf("%s|%s: field %s: expected %q, got %q", step.Table, step.Key, field, expected, actual)}
			}
		}
		return checkResult{StatusPassed, fmt.Sprintf("%s|%s: all fields match", step.Table, step.Key)}
	}

	return checkResult{StatusError, "no assertion mode specified"}
}

// ============================================================================
// verifyStateDBExecutor
// ============================================================================

type verifyStateDBExecutor struct{}

func (e *verifyStateDBExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	allPassed := true

	timeout := step.Expect.Timeout
	interval := step.Expect.PollInterval

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		stateClient := dev.Underlying().StateClient()
		if stateClient == nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: "STATE_DB client not connected",
			})
			allPassed = false
			continue
		}

		matched := false
		deadline := time.Now().Add(timeout)
		polls := 0

		for time.Now().Before(deadline) {
			polls++
			vals, err := stateClient.GetEntry(step.Table, step.Key)
			if err != nil {
				details = append(details, DeviceResult{
					Device: name, Status: StatusError,
					Message: fmt.Sprintf("reading STATE_DB: %s", err),
				})
				allPassed = false
				break
			}

			if vals != nil && matchFields(vals, step.Expect.Fields) {
				matched = true
				details = append(details, DeviceResult{
					Device: name, Status: StatusPassed,
					Message: fmt.Sprintf("%s|%s: all fields match (polled %d times)", step.Table, step.Key, polls),
				})
				break
			}

			select {
			case <-time.After(interval):
			case <-ctx.Done():
				details = append(details, DeviceResult{
					Device: name, Status: StatusError,
					Message: "interrupted",
				})
				allPassed = false
				break
			}
		}

		if !matched && allPassed {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("%s|%s: timeout after %s (%d polls)", step.Table, step.Key, timeout, polls),
			})
			allPassed = false
		}
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

func matchFields(actual map[string]string, expected map[string]string) bool {
	for k, v := range expected {
		if actual[k] != v {
			return false
		}
	}
	return true
}

// ============================================================================
// verifyBGPExecutor
// ============================================================================

type verifyBGPExecutor struct{}

func (e *verifyBGPExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	allPassed := true

	timeout := step.Expect.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	interval := step.Expect.PollInterval
	if interval == 0 {
		interval = 5 * time.Second
	}

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		matched := false
		deadline := time.Now().Add(timeout)
		polls := 0

		for time.Now().Before(deadline) {
			polls++

			// Run BGP health check
			results, err := dev.RunHealthChecks(ctx, "bgp")
			if err != nil {
				time.Sleep(interval)
				continue
			}

			bgpOK := true
			for _, hc := range results {
				if hc.Status != "pass" {
					bgpOK = false
					break
				}
			}

			if bgpOK {
				matched = true
				expectedState := "Established"
				if step.Expect != nil && step.Expect.State != "" {
					expectedState = step.Expect.State
				}
				details = append(details, DeviceResult{
					Device: name, Status: StatusPassed,
					Message: fmt.Sprintf("BGP %s (polled %d times)", expectedState, polls),
				})
				break
			}

			select {
			case <-time.After(interval):
			case <-ctx.Done():
				details = append(details, DeviceResult{
					Device: name, Status: StatusError,
					Message: "interrupted",
				})
				allPassed = false
				break
			}
		}

		if !matched && allPassed {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("BGP not converged after %s (%d polls)", timeout, polls),
			})
			allPassed = false
		}
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// ============================================================================
// verifyHealthExecutor
// ============================================================================

type verifyHealthExecutor struct{}

func (e *verifyHealthExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		results, err := dev.RunHealthChecks(ctx, "")
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("health check error: %s", err),
			})
			allPassed = false
			continue
		}

		passed := 0
		failed := 0
		for _, hc := range results {
			if hc.Status == "pass" {
				passed++
			} else {
				failed++
			}
		}

		if failed == 0 {
			details = append(details, DeviceResult{
				Device: name, Status: StatusPassed,
				Message: fmt.Sprintf("overall ok (%d checks passed)", passed),
			})
		} else {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("overall failed (%d passed, %d failed)", passed, failed),
			})
			allPassed = false
		}
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// ============================================================================
// verifyRouteExecutor
// ============================================================================

type verifyRouteExecutor struct{}

func (e *verifyRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	deviceName := r.resolveDevices(step)[0]
	dev, err := r.Network.GetDevice(deviceName)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Device: deviceName, Message: err.Error(),
		}}
	}

	timeout := step.Expect.Timeout
	interval := step.Expect.PollInterval
	deadline := time.Now().Add(timeout)
	polls := 0

	for time.Now().Before(deadline) {
		polls++
		var entry *device.RouteEntry
		var routeErr error

		if step.Expect.Source == "asic_db" {
			entry, routeErr = dev.GetRouteASIC(ctx, step.VRF, step.Prefix)
		} else {
			entry, routeErr = dev.GetRoute(ctx, step.VRF, step.Prefix)
		}

		if routeErr != nil {
			return &StepOutput{Result: &StepResult{
				Status: StatusError, Device: deviceName, Message: routeErr.Error(),
			}}
		}

		if entry != nil && matchRoute(entry, step.Expect) {
			source := "APP_DB"
			if step.Expect.Source == "asic_db" {
				source = "ASIC_DB"
			}
			msg := fmt.Sprintf("%s via %s (%s, %s, polled %d times)",
				step.Prefix, step.Expect.NextHopIP, step.Expect.Protocol, source, polls)
			return &StepOutput{Result: &StepResult{
				Status: StatusPassed, Device: deviceName, Message: msg,
			}}
		}

		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return &StepOutput{Result: &StepResult{
				Status: StatusError, Device: deviceName, Message: "interrupted",
			}}
		}
	}

	return &StepOutput{Result: &StepResult{
		Status:  StatusFailed,
		Device:  deviceName,
		Message: fmt.Sprintf("%s not found after %s (%d polls)", step.Prefix, timeout, polls),
	}}
}

// matchRoute returns true if the RouteEntry matches all non-empty expect fields.
func matchRoute(entry *device.RouteEntry, expect *ExpectBlock) bool {
	if expect.Protocol != "" && entry.Protocol != expect.Protocol {
		return false
	}
	if expect.NextHopIP != "" {
		found := false
		for _, nh := range entry.NextHops {
			if nh.IP == expect.NextHopIP {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ============================================================================
// verifyPingExecutor
// ============================================================================

type verifyPingExecutor struct{}

func (e *verifyPingExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	// Check platform dataplane capability
	if !r.hasDataplane() {
		platformName := r.scenario.Platform
		if r.opts.Platform != "" {
			platformName = r.opts.Platform
		}
		return &StepOutput{Result: &StepResult{
			Status:  StatusSkipped,
			Message: fmt.Sprintf("platform %s has dataplane: false", platformName),
		}}
	}

	deviceName := r.resolveDevices(step)[0]
	dev, err := r.Network.GetDevice(deviceName)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Device: deviceName, Message: err.Error(),
		}}
	}

	// Resolve target IP
	targetIP := step.Target
	if net.ParseIP(targetIP) == nil {
		// Treat as device name, resolve to loopback IP
		targetDev, err := r.Network.GetDevice(step.Target)
		if err != nil {
			return &StepOutput{Result: &StepResult{
				Status: StatusError, Device: deviceName,
				Message: fmt.Sprintf("target device %q: %s", step.Target, err),
			}}
		}
		targetIP = targetDev.Underlying().Profile.LoopbackIP
	}

	// Get SSH client from tunnel
	tunnel := dev.Underlying().Tunnel()
	if tunnel == nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Device: deviceName,
			Message: "no SSH tunnel available",
		}}
	}

	sshClient := tunnel.SSHClient()
	session, err := sshClient.NewSession()
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Device: deviceName,
			Message: fmt.Sprintf("SSH session: %s", err),
		}}
	}
	defer session.Close()

	count := step.Count
	if count == 0 {
		count = 5
	}

	output, err := session.CombinedOutput(fmt.Sprintf("ping -c %d -W 5 %s", count, targetIP))
	if err != nil && len(output) == 0 {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Device: deviceName,
			Message: fmt.Sprintf("ping command failed: %s", err),
		}}
	}

	// Parse packet loss from output
	successRate := parsePingSuccessRate(string(output))
	expectedRate := 1.0
	if step.Expect != nil && step.Expect.SuccessRate != nil {
		expectedRate = *step.Expect.SuccessRate
	}

	if successRate >= expectedRate {
		return &StepOutput{Result: &StepResult{
			Status: StatusPassed, Device: deviceName,
			Message: fmt.Sprintf("ping %s: %.0f%% success", targetIP, successRate*100),
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status: StatusFailed, Device: deviceName,
		Message: fmt.Sprintf("ping %s: %.0f%% success (expected ≥ %.0f%%)", targetIP, successRate*100, expectedRate*100),
	}}
}

var packetLossRe = regexp.MustCompile(`(\d+)% packet loss`)

func parsePingSuccessRate(output string) float64 {
	matches := packetLossRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return 0
	}
	loss, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}
	return 1.0 - (loss / 100.0)
}

// ============================================================================
// applyServiceExecutor
// ============================================================================

type applyServiceExecutor struct{}

func (e *applyServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	changeSets := make(map[string]*network.ChangeSet)
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		iface, err := dev.GetInterface(step.Interface)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting interface %s: %s", step.Interface, err),
			})
			allPassed = false
			continue
		}

		// Extract IP from params if present
		opts := network.ApplyServiceOpts{}
		if step.Params != nil {
			if ip, ok := step.Params["ip"]; ok {
				opts.IPAddress = fmt.Sprintf("%v", ip)
			}
		}

		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return iface.ApplyService(ctx, step.Service, opts)
		})
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("apply-service %s: %s", step.Service, err),
			})
			allPassed = false
			continue
		}

		changeSets[name] = cs
		details = append(details, DeviceResult{
			Device: name, Status: StatusPassed,
			Message: fmt.Sprintf("applied service %s on %s (%d changes)", step.Service, step.Interface, len(cs.Changes)),
		})
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{
		Result:     &StepResult{Status: status, Details: details},
		ChangeSets: changeSets,
	}
}

// ============================================================================
// removeServiceExecutor
// ============================================================================

type removeServiceExecutor struct{}

func (e *removeServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	changeSets := make(map[string]*network.ChangeSet)
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		iface, err := dev.GetInterface(step.Interface)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting interface %s: %s", step.Interface, err),
			})
			allPassed = false
			continue
		}

		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return iface.RemoveService(ctx)
		})
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("remove-service: %s", err),
			})
			allPassed = false
			continue
		}

		changeSets[name] = cs
		details = append(details, DeviceResult{
			Device: name, Status: StatusPassed,
			Message: fmt.Sprintf("removed service from %s (%d changes)", step.Interface, len(cs.Changes)),
		})
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{
		Result:     &StepResult{Status: status, Details: details},
		ChangeSets: changeSets,
	}
}

// ============================================================================
// applyBaselineExecutor
// ============================================================================

type applyBaselineExecutor struct{}

func (e *applyBaselineExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	changeSets := make(map[string]*network.ChangeSet)
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		// Convert vars map to "key=value" slice
		var vars []string
		for k, v := range step.Vars {
			vars = append(vars, fmt.Sprintf("%s=%s", k, v))
		}

		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.ApplyBaseline(ctx, step.Configlet, vars)
		})
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("apply-baseline %s: %s", step.Configlet, err),
			})
			allPassed = false
			continue
		}

		changeSets[name] = cs
		details = append(details, DeviceResult{
			Device: name, Status: StatusPassed,
			Message: fmt.Sprintf("applied baseline %s (%d changes)", step.Configlet, len(cs.Changes)),
		})
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{
		Result:     &StepResult{Status: status, Details: details},
		ChangeSets: changeSets,
	}
}

// ============================================================================
// sshCommandExecutor
// ============================================================================

type sshCommandExecutor struct{}

func (e *sshCommandExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		tunnel := dev.Underlying().Tunnel()
		if tunnel == nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: "no SSH tunnel available",
			})
			allPassed = false
			continue
		}

		output, err := tunnel.ExecCommand(step.Command)
		exitOK := err == nil

		if step.Expect != nil && step.Expect.Contains != "" {
			if strings.Contains(output, step.Expect.Contains) {
				details = append(details, DeviceResult{
					Device: name, Status: StatusPassed,
					Message: fmt.Sprintf("output contains %q", step.Expect.Contains),
				})
			} else {
				details = append(details, DeviceResult{
					Device: name, Status: StatusFailed,
					Message: fmt.Sprintf("output does not contain %q", step.Expect.Contains),
				})
				allPassed = false
			}
		} else {
			if exitOK {
				details = append(details, DeviceResult{
					Device: name, Status: StatusPassed,
					Message: "command succeeded",
				})
			} else {
				details = append(details, DeviceResult{
					Device: name, Status: StatusFailed,
					Message: fmt.Sprintf("command failed: %s", err),
				})
				allPassed = false
			}
		}
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// ============================================================================
// restartServiceExecutor
// ============================================================================

type restartServiceExecutor struct{}

func (e *restartServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		if err := dev.RestartService(ctx, step.Service); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("restart %s: %s", step.Service, err),
			})
			allPassed = false
			continue
		}

		details = append(details, DeviceResult{
			Device: name, Status: StatusPassed,
			Message: fmt.Sprintf("restarted %s", step.Service),
		})
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// ============================================================================
// applyFRRDefaultsExecutor
// ============================================================================

type applyFRRDefaultsExecutor struct{}

func (e *applyFRRDefaultsExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)
	var details []DeviceResult
	allPassed := true

	for _, name := range devices {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: fmt.Sprintf("getting device: %s", err),
			})
			allPassed = false
			continue
		}

		if err := dev.ApplyFRRDefaults(ctx); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: fmt.Sprintf("apply FRR defaults: %s", err),
			})
			allPassed = false
			continue
		}

		details = append(details, DeviceResult{
			Device: name, Status: StatusPassed,
			Message: "applied FRR defaults",
		})
	}

	status := StatusPassed
	if !allPassed {
		status = StatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}
