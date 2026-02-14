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
	ActionSetInterface:       &setInterfaceExecutor{},
	ActionCreateVLAN:         &createVLANExecutor{},
	ActionDeleteVLAN:         &deleteVLANExecutor{},
	ActionAddVLANMember:      &addVLANMemberExecutor{},
	ActionCreateVRF:          &createVRFExecutor{},
	ActionDeleteVRF:          &deleteVRFExecutor{},
	ActionSetupEVPN:          &setupEVPNExecutor{},
	ActionAddVRFInterface:    &addVRFInterfaceExecutor{},
	ActionRemoveVRFInterface: &removeVRFInterfaceExecutor{},
	ActionBindIPVPN:          &bindIPVPNExecutor{},
	ActionUnbindIPVPN:        &unbindIPVPNExecutor{},
	ActionBindMACVPN:         &bindMACVPNExecutor{},
	ActionUnbindMACVPN:       &unbindMACVPNExecutor{},
	ActionAddStaticRoute:     &addStaticRouteExecutor{},
	ActionRemoveStaticRoute:  &removeStaticRouteExecutor{},
	ActionRemoveVLANMember:   &removeVLANMemberExecutor{},
	ActionApplyQoS:           &applyQoSExecutor{},
	ActionRemoveQoS:          &removeQoSExecutor{},
	ActionConfigureSVI:       &configureSVIExecutor{},
	ActionBGPAddNeighbor:     &bgpAddNeighborExecutor{},
	ActionBGPRemoveNeighbor:  &bgpRemoveNeighborExecutor{},
	ActionRefreshService:     &refreshServiceExecutor{},
	ActionCleanup:            &cleanupExecutor{},
}

// strParam extracts a string parameter from the step's Params map.
func strParam(params map[string]any, key string) string {
	v, ok := params[key]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// intParam extracts an integer parameter from the step's Params map.
// Handles float64 (YAML default for numbers) and string representations.
func intParam(params map[string]any, key string) int {
	v, ok := params[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return n
	default:
		return 0
	}
}

// boolParam extracts a boolean parameter from the step's Params map.
func boolParam(params map[string]any, key string) bool {
	v, ok := params[key]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true" || val == "1"
	default:
		return false
	}
}

// executeForDevices runs an operation on each target device and collects results.
// The callback fn receives the device and its name, returning an optional ChangeSet,
// a human-readable message, and an error. Executors that use ExecuteOp should call
// it inside fn.
func (r *Runner) executeForDevices(step *Step, fn func(dev *network.Device, name string) (*network.ChangeSet, string, error)) *StepOutput {
	names := r.resolveDevices(step)
	details := make([]DeviceResult, 0, len(names))
	changeSets := make(map[string]*network.ChangeSet)
	allPassed := true

	for _, name := range names {
		dev, err := r.Network.GetDevice(name)
		if err != nil {
			details = append(details, DeviceResult{Device: name, Status: StatusError, Message: err.Error()})
			allPassed = false
			continue
		}
		cs, msg, err := fn(dev, name)
		if err != nil {
			details = append(details, DeviceResult{Device: name, Status: StatusError, Message: err.Error()})
			allPassed = false
			continue
		}
		if cs != nil {
			changeSets[name] = cs
			r.ChangeSets[name] = cs
		}
		details = append(details, DeviceResult{Device: name, Status: StatusPassed, Message: msg})
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

// pollUntil polls fn at the given interval until it returns true, the timeout
// expires, or ctx is cancelled.
func pollUntil(ctx context.Context, timeout, interval time.Duration, fn func() (done bool, err error)) error {
	deadline := time.Now().Add(timeout)
pollLoop:
	for time.Now().Before(deadline) {
		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			break pollLoop
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("timeout after %s", timeout)
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
		polls := 0
		var pollErr error

		err = pollUntil(ctx, timeout, interval, func() (bool, error) {
			polls++
			vals, err := stateClient.GetEntry(step.Table, step.Key)
			if err != nil {
				pollErr = err
				return false, fmt.Errorf("reading STATE_DB: %s", err)
			}
			if vals != nil && matchFields(vals, step.Expect.Fields) {
				matched = true
				return true, nil
			}
			return false, nil
		})

		if pollErr != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StatusError,
				Message: pollErr.Error(),
			})
			allPassed = false
		} else if matched {
			details = append(details, DeviceResult{
				Device: name, Status: StatusPassed,
				Message: fmt.Sprintf("%s|%s: all fields match (polled %d times)", step.Table, step.Key, polls),
			})
		} else {
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
	interval := step.Expect.PollInterval
	expectedState := step.Expect.State

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
		polls := 0
		var lastResults []network.HealthCheckResult

		err = pollUntil(ctx, timeout, interval, func() (bool, error) {
			polls++

			// Run BGP health check
			results, err := dev.RunHealthChecks(ctx, "bgp")
			if err != nil {
				return false, nil // transient error, keep polling
			}
			lastResults = results

			// Check if all peers are passing health checks
			bgpOK := true
			for _, hc := range results {
				if hc.Status != "pass" {
					bgpOK = false
					break
				}
			}

			if !bgpOK {
				return false, nil
			}

			// Health checks pass — now verify against expectedState.
			// The health check messages contain the peer state (e.g.,
			// "BGP neighbor 10.1.0.1 (vrf default): Established").
			// If expectedState is "Established" (the default), health check
			// pass already implies it. For non-default states, check messages.
			if expectedState != "Established" {
				for _, hc := range results {
					if !strings.Contains(hc.Message, expectedState) {
						return false, nil
					}
				}
			}

			matched = true
			return true, nil
		})

		if matched {
			details = append(details, DeviceResult{
				Device: name, Status: StatusPassed,
				Message: fmt.Sprintf("BGP %s (polled %d times)", expectedState, polls),
			})
		} else {
			// Build a diagnostic message from last health check results
			msg := fmt.Sprintf("BGP not converged after %s (%d polls)", timeout, polls)
			if len(lastResults) > 0 {
				var states []string
				for _, hc := range lastResults {
					states = append(states, hc.Message)
				}
				msg += ": " + strings.Join(states, "; ")
			}
			details = append(details, DeviceResult{
				Device: name, Status: StatusFailed,
				Message: msg,
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
	polls := 0
	var matched bool
	var matchMsg string

	pollErr := pollUntil(ctx, timeout, interval, func() (bool, error) {
		polls++
		var entry *device.RouteEntry
		var routeErr error

		if step.Expect.Source == "asic_db" {
			entry, routeErr = dev.GetRouteASIC(ctx, step.VRF, step.Prefix)
		} else {
			entry, routeErr = dev.GetRoute(ctx, step.VRF, step.Prefix)
		}

		if routeErr != nil {
			return false, routeErr
		}

		if entry != nil && matchRoute(entry, step.Expect) {
			source := "APP_DB"
			if step.Expect.Source == "asic_db" {
				source = "ASIC_DB"
			}
			matchMsg = fmt.Sprintf("%s via %s (%s, %s, polled %d times)",
				step.Prefix, step.Expect.NextHopIP, step.Expect.Protocol, source, polls)
			matched = true
			return true, nil
		}
		return false, nil
	})

	if matched {
		return &StepOutput{Result: &StepResult{
			Status: StatusPassed, Device: deviceName, Message: matchMsg,
		}}
	}

	if pollErr != nil && !strings.HasPrefix(pollErr.Error(), "timeout after") {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Device: deviceName, Message: pollErr.Error(),
		}}
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
	opts := network.ApplyServiceOpts{}
	if step.Params != nil {
		if ip, ok := step.Params["ip"]; ok {
			opts.IPAddress = fmt.Sprintf("%v", ip)
		}
	}
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		iface, err := dev.GetInterface(step.Interface)
		if err != nil {
			return nil, "", fmt.Errorf("getting interface %s: %s", step.Interface, err)
		}
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return iface.ApplyService(ctx, step.Service, opts)
		})
		if err != nil {
			return nil, "", fmt.Errorf("apply-service %s: %s", step.Service, err)
		}
		return cs, fmt.Sprintf("applied service %s on %s (%d changes)", step.Service, step.Interface, len(cs.Changes)), nil
	})
}

// ============================================================================
// removeServiceExecutor
// ============================================================================

type removeServiceExecutor struct{}

func (e *removeServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		iface, err := dev.GetInterface(step.Interface)
		if err != nil {
			return nil, "", fmt.Errorf("getting interface %s: %s", step.Interface, err)
		}
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return iface.RemoveService(ctx)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-service: %s", err)
		}
		return cs, fmt.Sprintf("removed service from %s (%d changes)", step.Interface, len(cs.Changes)), nil
	})
}

// ============================================================================
// applyBaselineExecutor
// ============================================================================

type applyBaselineExecutor struct{}

func (e *applyBaselineExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	var vars []string
	for k, v := range step.Vars {
		vars = append(vars, fmt.Sprintf("%s=%s", k, v))
	}
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.ApplyBaseline(ctx, step.Configlet, vars)
		})
		if err != nil {
			return nil, "", fmt.Errorf("apply-baseline %s: %s", step.Configlet, err)
		}
		return cs, fmt.Sprintf("applied baseline %s (%d changes)", step.Configlet, len(cs.Changes)), nil
	})
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
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		if err := dev.RestartService(ctx, step.Service); err != nil {
			return nil, "", fmt.Errorf("restart %s: %s", step.Service, err)
		}
		return nil, fmt.Sprintf("restarted %s", step.Service), nil
	})
}

// ============================================================================
// applyFRRDefaultsExecutor
// ============================================================================

type applyFRRDefaultsExecutor struct{}

func (e *applyFRRDefaultsExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		if err := dev.ApplyFRRDefaults(ctx); err != nil {
			return nil, "", fmt.Errorf("apply FRR defaults: %s", err)
		}
		return nil, "applied FRR defaults", nil
	})
}

// ============================================================================
// setInterfaceExecutor
// ============================================================================

type setInterfaceExecutor struct{}

func (e *setInterfaceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	property := strParam(step.Params, "property")
	value := strParam(step.Params, "value")

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		iface, err := dev.GetInterface(step.Interface)
		if err != nil {
			return nil, "", fmt.Errorf("getting interface %s: %s", step.Interface, err)
		}

		var cs *network.ChangeSet
		switch property {
		case "ip":
			cs, err = dev.ExecuteOp(func() (*network.ChangeSet, error) {
				return iface.SetIP(ctx, value)
			})
		case "vrf":
			cs, err = dev.ExecuteOp(func() (*network.ChangeSet, error) {
				return iface.SetVRF(ctx, value)
			})
		default:
			cs, err = dev.ExecuteOp(func() (*network.ChangeSet, error) {
				return iface.Set(ctx, property, value)
			})
		}
		if err != nil {
			return nil, "", fmt.Errorf("set-interface %s %s=%s: %s", step.Interface, property, value, err)
		}
		return cs, fmt.Sprintf("set %s %s=%s (%d changes)", step.Interface, property, value, len(cs.Changes)), nil
	})
}

// ============================================================================
// createVLANExecutor
// ============================================================================

type createVLANExecutor struct{}

func (e *createVLANExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := intParam(step.Params, "vlan_id")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.CreateVLAN(ctx, vlanID, network.VLANConfig{})
		})
		if err != nil {
			return nil, "", fmt.Errorf("create-vlan %d: %s", vlanID, err)
		}
		return cs, fmt.Sprintf("created VLAN %d (%d changes)", vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// deleteVLANExecutor
// ============================================================================

type deleteVLANExecutor struct{}

func (e *deleteVLANExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := intParam(step.Params, "vlan_id")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.DeleteVLAN(ctx, vlanID)
		})
		if err != nil {
			return nil, "", fmt.Errorf("delete-vlan %d: %s", vlanID, err)
		}
		return cs, fmt.Sprintf("deleted VLAN %d (%d changes)", vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// addVLANMemberExecutor
// ============================================================================

type addVLANMemberExecutor struct{}

func (e *addVLANMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := intParam(step.Params, "vlan_id")
	interfaceName := strParam(step.Params, "interface")
	tagged := boolParam(step.Params, "tagged")

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.AddVLANMember(ctx, vlanID, interfaceName, tagged)
		})
		if err != nil {
			return nil, "", fmt.Errorf("add-vlan-member %d %s: %s", vlanID, interfaceName, err)
		}
		return cs, fmt.Sprintf("added %s to VLAN %d (%d changes)", interfaceName, vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// createVRFExecutor
// ============================================================================

type createVRFExecutor struct{}

func (e *createVRFExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.CreateVRF(ctx, vrfName, network.VRFConfig{})
		})
		if err != nil {
			return nil, "", fmt.Errorf("create-vrf %s: %s", vrfName, err)
		}
		return cs, fmt.Sprintf("created VRF %s (%d changes)", vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// deleteVRFExecutor
// ============================================================================

type deleteVRFExecutor struct{}

func (e *deleteVRFExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.DeleteVRF(ctx, vrfName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("delete-vrf %s: %s", vrfName, err)
		}
		return cs, fmt.Sprintf("deleted VRF %s (%d changes)", vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// setupEVPNExecutor
// ============================================================================

type setupEVPNExecutor struct{}

func (e *setupEVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	sourceIP := strParam(step.Params, "source_ip")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.SetupEVPN(ctx, sourceIP)
		})
		if err != nil {
			return nil, "", fmt.Errorf("setup-evpn (source=%s): %s", sourceIP, err)
		}
		return cs, fmt.Sprintf("setup EVPN (source=%s, %d changes)", sourceIP, len(cs.Changes)), nil
	})
}

// ============================================================================
// addVRFInterfaceExecutor
// ============================================================================

type addVRFInterfaceExecutor struct{}

func (e *addVRFInterfaceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	intfName := strParam(step.Params, "interface")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.AddVRFInterface(ctx, vrfName, intfName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("add-vrf-interface %s %s: %s", vrfName, intfName, err)
		}
		return cs, fmt.Sprintf("added %s to VRF %s (%d changes)", intfName, vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// removeVRFInterfaceExecutor
// ============================================================================

type removeVRFInterfaceExecutor struct{}

func (e *removeVRFInterfaceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	intfName := strParam(step.Params, "interface")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.RemoveVRFInterface(ctx, vrfName, intfName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-vrf-interface %s %s: %s", vrfName, intfName, err)
		}
		return cs, fmt.Sprintf("removed %s from VRF %s (%d changes)", intfName, vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// bindIPVPNExecutor
// ============================================================================

type bindIPVPNExecutor struct{}

func (e *bindIPVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	ipvpnName := strParam(step.Params, "ipvpn")

	ipvpnDef, err := r.Network.GetIPVPN(ipvpnName)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Message: fmt.Sprintf("IP-VPN lookup: %s", err),
		}}
	}

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.BindIPVPN(ctx, vrfName, ipvpnDef)
		})
		if err != nil {
			return nil, "", fmt.Errorf("bind-ipvpn %s %s: %s", vrfName, ipvpnName, err)
		}
		return cs, fmt.Sprintf("bound IP-VPN %s to VRF %s (%d changes)", ipvpnName, vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// unbindIPVPNExecutor
// ============================================================================

type unbindIPVPNExecutor struct{}

func (e *unbindIPVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.UnbindIPVPN(ctx, vrfName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("unbind-ipvpn %s: %s", vrfName, err)
		}
		return cs, fmt.Sprintf("unbound IP-VPN from VRF %s (%d changes)", vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// bindMACVPNExecutor
// ============================================================================

type bindMACVPNExecutor struct{}

func (e *bindMACVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := intParam(step.Params, "vlan_id")
	macvpnName := strParam(step.Params, "macvpn")

	macvpnDef, err := r.Network.GetMACVPN(macvpnName)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Message: fmt.Sprintf("MAC-VPN lookup: %s", err),
		}}
	}

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.MapL2VNI(ctx, vlanID, macvpnDef.L2VNI)
		})
		if err != nil {
			return nil, "", fmt.Errorf("bind-macvpn vlan=%d %s: %s", vlanID, macvpnName, err)
		}
		return cs, fmt.Sprintf("bound MAC-VPN %s to VLAN %d (%d changes)", macvpnName, vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// unbindMACVPNExecutor
// ============================================================================

type unbindMACVPNExecutor struct{}

func (e *unbindMACVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := intParam(step.Params, "vlan_id")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.UnmapL2VNI(ctx, vlanID)
		})
		if err != nil {
			return nil, "", fmt.Errorf("unbind-macvpn vlan=%d: %s", vlanID, err)
		}
		return cs, fmt.Sprintf("unbound MAC-VPN from VLAN %d (%d changes)", vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// addStaticRouteExecutor
// ============================================================================

type addStaticRouteExecutor struct{}

func (e *addStaticRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	prefix := strParam(step.Params, "prefix")
	nextHop := strParam(step.Params, "next_hop")
	metric := intParam(step.Params, "metric")

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.AddStaticRoute(ctx, vrfName, prefix, nextHop, metric)
		})
		if err != nil {
			return nil, "", fmt.Errorf("add-static-route %s %s via %s: %s", vrfName, prefix, nextHop, err)
		}
		return cs, fmt.Sprintf("added static route %s via %s in %s (%d changes)", prefix, nextHop, vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// removeStaticRouteExecutor
// ============================================================================

type removeStaticRouteExecutor struct{}

func (e *removeStaticRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := strParam(step.Params, "vrf")
	prefix := strParam(step.Params, "prefix")

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.RemoveStaticRoute(ctx, vrfName, prefix)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-static-route %s %s: %s", vrfName, prefix, err)
		}
		return cs, fmt.Sprintf("removed static route %s from %s (%d changes)", prefix, vrfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// removeVLANMemberExecutor
// ============================================================================

type removeVLANMemberExecutor struct{}

func (e *removeVLANMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := intParam(step.Params, "vlan_id")
	interfaceName := strParam(step.Params, "interface")

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.RemoveVLANMember(ctx, vlanID, interfaceName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-vlan-member %d %s: %s", vlanID, interfaceName, err)
		}
		return cs, fmt.Sprintf("removed %s from VLAN %d (%d changes)", interfaceName, vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// applyQoSExecutor
// ============================================================================

type applyQoSExecutor struct{}

func (e *applyQoSExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	intfName := strParam(step.Params, "interface")
	policyName := strParam(step.Params, "qos_policy")

	policy, err := r.Network.GetQoSPolicy(policyName)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StatusError, Message: fmt.Sprintf("QoS policy lookup: %s", err),
		}}
	}

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.ApplyQoS(ctx, intfName, policyName, policy)
		})
		if err != nil {
			return nil, "", fmt.Errorf("apply-qos %s %s: %s", intfName, policyName, err)
		}
		return cs, fmt.Sprintf("applied QoS policy %s on %s (%d changes)", policyName, intfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// removeQoSExecutor
// ============================================================================

type removeQoSExecutor struct{}

func (e *removeQoSExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	intfName := strParam(step.Params, "interface")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.RemoveQoS(ctx, intfName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-qos %s: %s", intfName, err)
		}
		return cs, fmt.Sprintf("removed QoS from %s (%d changes)", intfName, len(cs.Changes)), nil
	})
}

// ============================================================================
// configureSVIExecutor
// ============================================================================

type configureSVIExecutor struct{}

func (e *configureSVIExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := intParam(step.Params, "vlan_id")
	opts := network.SVIConfig{
		VRF:       strParam(step.Params, "vrf"),
		IPAddress: strParam(step.Params, "ip"),
	}
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return dev.ConfigureSVI(ctx, vlanID, opts)
		})
		if err != nil {
			return nil, "", fmt.Errorf("configure-svi vlan=%d: %s", vlanID, err)
		}
		return cs, fmt.Sprintf("configured SVI Vlan%d (%d changes)", vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// bgpAddNeighborExecutor
// ============================================================================

type bgpAddNeighborExecutor struct{}

func (e *bgpAddNeighborExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	remoteASN := intParam(step.Params, "remote_asn")
	neighborIP := strParam(step.Params, "neighbor_ip")

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		var cs *network.ChangeSet
		var err error
		if step.Interface != "" {
			iface, ifErr := dev.GetInterface(step.Interface)
			if ifErr != nil {
				return nil, "", fmt.Errorf("getting interface %s: %s", step.Interface, ifErr)
			}
			cfg := network.DirectBGPNeighborConfig{
				NeighborIP: neighborIP,
				RemoteAS:   remoteASN,
			}
			cs, err = dev.ExecuteOp(func() (*network.ChangeSet, error) {
				return iface.AddBGPNeighbor(ctx, cfg)
			})
		} else {
			cs, err = dev.ExecuteOp(func() (*network.ChangeSet, error) {
				return dev.AddLoopbackBGPNeighbor(ctx, neighborIP, remoteASN, "", false)
			})
		}
		if err != nil {
			return nil, "", fmt.Errorf("bgp-add-neighbor %s: %s", neighborIP, err)
		}
		return cs, fmt.Sprintf("added BGP neighbor %s ASN %d (%d changes)", neighborIP, remoteASN, len(cs.Changes)), nil
	})
}

// ============================================================================
// bgpRemoveNeighborExecutor
// ============================================================================

type bgpRemoveNeighborExecutor struct{}

func (e *bgpRemoveNeighborExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	neighborIP := strParam(step.Params, "neighbor_ip")

	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		var cs *network.ChangeSet
		var err error
		if step.Interface != "" {
			iface, ifErr := dev.GetInterface(step.Interface)
			if ifErr != nil {
				return nil, "", fmt.Errorf("getting interface %s: %s", step.Interface, ifErr)
			}
			cs, err = dev.ExecuteOp(func() (*network.ChangeSet, error) {
				return iface.RemoveBGPNeighbor(ctx, neighborIP)
			})
		} else {
			cs, err = dev.ExecuteOp(func() (*network.ChangeSet, error) {
				return dev.RemoveBGPNeighbor(ctx, neighborIP)
			})
		}
		if err != nil {
			return nil, "", fmt.Errorf("bgp-remove-neighbor %s: %s", neighborIP, err)
		}
		return cs, fmt.Sprintf("removed BGP neighbor %s (%d changes)", neighborIP, len(cs.Changes)), nil
	})
}

// ============================================================================
// refreshServiceExecutor
// ============================================================================

type refreshServiceExecutor struct{}

func (e *refreshServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		iface, err := dev.GetInterface(step.Interface)
		if err != nil {
			return nil, "", fmt.Errorf("getting interface %s: %s", step.Interface, err)
		}
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			return iface.RefreshService(ctx)
		})
		if err != nil {
			return nil, "", fmt.Errorf("refresh-service %s: %s", step.Interface, err)
		}
		return cs, fmt.Sprintf("refreshed service on %s (%d changes)", step.Interface, len(cs.Changes)), nil
	})
}

// ============================================================================
// cleanupExecutor
// ============================================================================

type cleanupExecutor struct{}

func (e *cleanupExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	cleanupType := strParam(step.Params, "type")
	return r.executeForDevices(step, func(dev *network.Device, _ string) (*network.ChangeSet, string, error) {
		var summary *network.CleanupSummary
		cs, err := dev.ExecuteOp(func() (*network.ChangeSet, error) {
			cs, s, err := dev.Cleanup(ctx, cleanupType)
			summary = s
			return cs, err
		})
		if err != nil {
			return nil, "", fmt.Errorf("cleanup: %s", err)
		}
		msg := fmt.Sprintf("cleanup (%d changes)", len(cs.Changes))
		if summary != nil {
			orphans := len(summary.OrphanedACLs) + len(summary.OrphanedVRFs) + len(summary.OrphanedVNIMappings)
			msg = fmt.Sprintf("cleanup: %d orphans removed (%d changes)", orphans, len(cs.Changes))
		}
		return cs, msg, nil
	})
}
