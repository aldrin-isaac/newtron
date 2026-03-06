package newtrun

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/util"
)

// stepExecutor executes a single step and returns output.
type stepExecutor interface {
	Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
}

// StepOutput is the return value from every executor.
type StepOutput struct {
	Result     *StepResult
	Composites map[string]string
}

// execOptsRun is a shorthand for ExecOpts with Execute: true.
var execOptsRun = newtron.ExecOpts{Execute: true}

// executors maps each StepAction to its executor implementation.
var executors = map[StepAction]stepExecutor{
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
	ActionConfigureLoopback:  &configureLoopbackExecutor{},
	ActionSSHCommand:         &sshCommandExecutor{},
	ActionRestartService:     &restartServiceExecutor{},
	ActionConfigReload:       &configReloadExecutor{},
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
	ActionRefreshService:          &refreshServiceExecutor{},
	ActionCleanup:                 &cleanupExecutor{},
	ActionCreatePortChannel:       &createPortChannelExecutor{},
	ActionDeletePortChannel:       &deletePortChannelExecutor{},
	ActionAddPortChannelMember:    &addPortChannelMemberExecutor{},
	ActionRemovePortChannelMember: &removePortChannelMemberExecutor{},

	// Host test executors
	ActionHostExec: &hostExecExecutor{},

	// ACL management executors
	ActionCreateACLTable: &createACLTableExecutor{},
	ActionAddACLRule:     &addACLRuleExecutor{},
	ActionDeleteACLRule:  &deleteACLRuleExecutor{},
	ActionDeleteACLTable: &deleteACLTableExecutor{},
	ActionBindACL:        &bindACLExecutor{},
	ActionUnbindACL:      &unbindACLExecutor{},
	ActionRemoveSVI:      &removeSVIExecutor{},
	ActionRemoveIP:       &removeIPExecutor{},
	ActionTeardownEVPN:     &teardownEVPNExecutor{},
	ActionConfigureBGP:     &configureBGPExecutor{},
	ActionRemoveBGPGlobals: &removeBGPGlobalsExecutor{},
	ActionRemoveLoopback:   &removeLoopbackExecutor{},
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
		n, err := strconv.Atoi(val)
		if err != nil {
			util.Logger.Warnf("intParam: invalid integer value for %q: %q (using 0)", key, val)
		}
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

// strSliceParam extracts a string slice parameter from the step's Params map.
// Handles []any (YAML default for lists) by converting each element to a string.
func strSliceParam(params map[string]any, key string) []string {
	v, ok := params[key]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result
	case []string:
		return val
	default:
		return nil
	}
}

// executeForDevices runs an operation on each target device and collects results.
// The callback fn receives the device name, returning a human-readable message and an error.
func (r *Runner) executeForDevices(step *Step, fn func(name string) (string, error)) *StepOutput {
	names := r.resolveDevices(step)
	if len(names) == 0 {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusError,
			Details: []DeviceResult{{Device: "(none)", Status: StepStatusError, Message: "no devices resolved"}},
		}}
	}
	details := make([]DeviceResult, 0, len(names))
	allPassed := true

	for _, name := range names {
		// Skip host devices for SONiC-specific operations (provision, restart-service, apply-frr-defaults, etc.)
		// Host actions use the 'command' executor which doesn't call this helper.
		if _, isHost := r.HostConns[name]; isHost {
			details = append(details, DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC operation not applicable)"})
			continue
		}
		msg, err := fn(name)
		if err != nil {
			details = append(details, DeviceResult{Device: name, Status: StepStatusError, Message: err.Error()})
			allPassed = false
			continue
		}
		details = append(details, DeviceResult{Device: name, Status: StepStatusPassed, Message: msg})
	}

	status := StepStatusPassed
	if !allPassed {
		status = StepStatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
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

// checkForDevices resolves devices, calls fn for each, and collects results.
// Use for non-polling verification executors. The callback returns status and message.
func (r *Runner) checkForDevices(step *Step, fn func(name string) (StepStatus, string)) *StepOutput {
	names := r.resolveDevices(step)
	if len(names) == 0 {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusError,
			Details: []DeviceResult{{Device: "(none)", Status: StepStatusError, Message: "no devices resolved"}},
		}}
	}
	details := make([]DeviceResult, 0, len(names))
	allPassed := true

	for _, name := range names {
		// Skip host devices for SONiC-specific verification (verify-health, verify-config-db, etc.)
		// Host actions use the 'host-exec' executor which doesn't call this helper.
		if _, isHost := r.HostConns[name]; isHost {
			details = append(details, DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC verification not applicable)"})
			continue
		}
		status, msg := fn(name)
		details = append(details, DeviceResult{Device: name, Status: status, Message: msg})
		if status != StepStatusPassed {
			allPassed = false
		}
	}

	status := StepStatusPassed
	if !allPassed {
		status = StepStatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// pollForDevices resolves devices, polls fn for each with timeout, and collects results.
// The callback returns (done, message, error). On done=true, message is the success message.
// On timeout, the last message is used as the failure detail.
func (r *Runner) pollForDevices(ctx context.Context, step *Step, fn func(name string) (done bool, msg string, err error)) *StepOutput {
	names := r.resolveDevices(step)
	if len(names) == 0 {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusError,
			Details: []DeviceResult{{Device: "(none)", Status: StepStatusError, Message: "no devices resolved"}},
		}}
	}
	details := make([]DeviceResult, 0, len(names))
	allPassed := true

	timeout := step.Expect.Timeout
	interval := step.Expect.PollInterval

	for _, name := range names {
		// Skip host devices for SONiC-specific verification (verify-bgp, verify-health, etc.)
		// Host actions use the 'host-exec' executor which doesn't call this helper.
		if _, isHost := r.HostConns[name]; isHost {
			details = append(details, DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC verification not applicable)"})
			continue
		}

		var matched bool
		var lastMsg string
		var pollErr error

		pollErr = pollUntil(ctx, timeout, interval, func() (bool, error) {
			done, msg, err := fn(name)
			lastMsg = msg
			if err != nil {
				return false, err
			}
			if done {
				matched = true
				return true, nil
			}
			return false, nil
		})

		if pollErr != nil && !strings.HasPrefix(pollErr.Error(), "timeout after") {
			details = append(details, DeviceResult{Device: name, Status: StepStatusError, Message: pollErr.Error()})
			allPassed = false
		} else if matched {
			details = append(details, DeviceResult{Device: name, Status: StepStatusPassed, Message: lastMsg})
		} else {
			if lastMsg == "" {
				lastMsg = fmt.Sprintf("timeout after %s", timeout)
			}
			details = append(details, DeviceResult{Device: name, Status: StepStatusFailed, Message: lastMsg})
			allPassed = false
		}
	}

	status := StepStatusPassed
	if !allPassed {
		status = StepStatusFailed
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// ============================================================================
// provisionExecutor
// ============================================================================

type provisionExecutor struct{}

func (e *provisionExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	devices := r.resolveDevices(step)

	var details []DeviceResult
	composites := make(map[string]string)
	allPassed := true

	for _, name := range devices {
		// Skip host devices — they don't have SONiC CONFIG_DB to provision
		if _, isHost := r.HostConns[name]; isHost {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusSkipped,
				Message: "host device (no SONiC provisioning)",
			})
			continue
		}

		handle, err := r.Client.GenerateComposite(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("generate composite: %s", err),
			})
			allPassed = false
			continue
		}

		// Best-effort config reload to restore CONFIG_DB to saved defaults.
		// On fresh boot: services may still be starting (SwSS not ready),
		// and CONFIG_DB is already in factory state — failure is safe.
		// On re-provision: reload succeeds, giving a clean baseline.
		if err := r.Client.ConfigReload(name); err != nil {
			util.Logger.Warnf("[%s] config reload before provision skipped: %v", name, err)
		} else {
			if err := r.Client.RefreshWithRetry(name, 60*time.Second); err != nil {
				details = append(details, DeviceResult{
					Device: name, Status: StepStatusFailed,
					Message: fmt.Sprintf("refresh after config reload: %s", err),
				})
				allPassed = false
				continue
			}
		}

		// Deliver composite — server handles lock→deliver→unlock internally
		result, err := r.Client.DeliverComposite(name, handle.Handle, "overwrite")
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("deliver composite: %s", err),
			})
			allPassed = false
			continue
		}

		// Refresh the device's cached CONFIG_DB and interface list so
		// subsequent steps see the newly provisioned PORT entries.
		if err := r.Client.Refresh(name); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("refresh after provision: %s", err),
			})
			allPassed = false
			continue
		}

		// Persist to config_db.json so subsequent config-reload steps
		// re-read the provisioned config (not factory defaults).
		if err := r.Client.SaveConfig(name); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("config save after provision: %s", err),
			})
			allPassed = false
			continue
		}

		composites[name] = handle.Handle
		details = append(details, DeviceResult{
			Device: name, Status: StepStatusPassed,
			Message: fmt.Sprintf("provisioned (%d entries applied)", result.Applied),
		})
	}

	status := StepStatusPassed
	if !allPassed {
		status = StepStatusFailed
	}
	return &StepOutput{
		Result:     &StepResult{Status: status, Details: details},
		Composites: composites,
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
			Status: StepStatusError, Message: "interrupted",
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status:  StepStatusPassed,
		Message: fmt.Sprintf("%s elapsed", step.Duration),
	}}
}

// ============================================================================
// verifyProvisioningExecutor
// ============================================================================

type verifyProvisioningExecutor struct{}

func (e *verifyProvisioningExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.checkForDevices(step, func(name string) (StepStatus, string) {
		handle, ok := r.Composites[name]
		if !ok {
			return StepStatusError, "no composite accumulated (was provision run first?)"
		}
		vr, err := r.Client.VerifyComposite(name, handle)
		if err != nil {
			return StepStatusError, fmt.Sprintf("verification error: %s", err)
		}
		total := vr.Passed + vr.Failed
		if vr.Failed == 0 {
			return StepStatusPassed, fmt.Sprintf("%d/%d CONFIG_DB entries verified", vr.Passed, total)
		}
		return StepStatusFailed, fmt.Sprintf("%d/%d CONFIG_DB entries verified (%d failed)", vr.Passed, total, vr.Failed)
	})
}

// ============================================================================
// verifyConfigDBExecutor
// ============================================================================

type verifyConfigDBExecutor struct{}

func (e *verifyConfigDBExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.checkForDevices(step, func(name string) (StepStatus, string) {
		result := e.checkDevice(r, name, step)
		return result.status, result.message
	})
}

type checkResult struct {
	status  StepStatus
	message string
}

func (e *verifyConfigDBExecutor) checkDevice(r *Runner, device string, step *Step) checkResult {
	// Mode 1: min_entries
	if step.Expect.MinEntries != nil {
		var count int
		if step.Key == "" {
			// No key: count all entries in the table
			keys, err := r.Client.ConfigDBTableKeys(device, step.Table)
			if err != nil {
				return checkResult{StepStatusError, fmt.Sprintf("scanning %s: %s", step.Table, err)}
			}
			count = len(keys)
		} else {
			// Specific key: count fields in that entry
			vals, err := r.Client.QueryConfigDB(device, step.Table, step.Key)
			if err != nil {
				return checkResult{StepStatusError, fmt.Sprintf("reading %s|%s: %s", step.Table, step.Key, err)}
			}
			count = len(vals)
		}
		if count >= *step.Expect.MinEntries {
			return checkResult{StepStatusPassed, fmt.Sprintf("%s: %d entries (≥ %d)", step.Table, count, *step.Expect.MinEntries)}
		}
		return checkResult{StepStatusFailed, fmt.Sprintf("%s: %d entries (expected ≥ %d)", step.Table, count, *step.Expect.MinEntries)}
	}

	// Mode 2: exists
	if step.Expect.Exists != nil {
		exists, err := r.Client.ConfigDBEntryExists(device, step.Table, step.Key)
		if err != nil {
			return checkResult{StepStatusError, fmt.Sprintf("checking %s|%s: %s", step.Table, step.Key, err)}
		}
		if exists == *step.Expect.Exists {
			return checkResult{StepStatusPassed, fmt.Sprintf("%s|%s: exists=%v", step.Table, step.Key, exists)}
		}
		return checkResult{StepStatusFailed, fmt.Sprintf("%s|%s: expected exists=%v, got %v", step.Table, step.Key, *step.Expect.Exists, exists)}
	}

	// Mode 3: fields
	if len(step.Expect.Fields) > 0 {
		vals, err := r.Client.QueryConfigDB(device, step.Table, step.Key)
		if err != nil {
			return checkResult{StepStatusError, fmt.Sprintf("reading %s|%s: %s", step.Table, step.Key, err)}
		}
		if len(vals) == 0 {
			return checkResult{StepStatusFailed, fmt.Sprintf("%s|%s: not found", step.Table, step.Key)}
		}
		for field, expected := range step.Expect.Fields {
			actual, ok := vals[field]
			if !ok {
				return checkResult{StepStatusFailed, fmt.Sprintf("%s|%s: field %s missing", step.Table, step.Key, field)}
			}
			if actual != expected {
				return checkResult{StepStatusFailed, fmt.Sprintf("%s|%s: field %s: expected %q, got %q", step.Table, step.Key, field, expected, actual)}
			}
		}
		return checkResult{StepStatusPassed, fmt.Sprintf("%s|%s: all fields match", step.Table, step.Key)}
	}

	return checkResult{StepStatusError, "no assertion mode specified"}
}

// ============================================================================
// verifyStateDBExecutor
// ============================================================================

type verifyStateDBExecutor struct{}

func (e *verifyStateDBExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.pollForDevices(ctx, step, func(name string) (bool, string, error) {
		vals, err := r.Client.QueryStateDB(name, step.Table, step.Key)
		if err != nil {
			return false, "", fmt.Errorf("reading STATE_DB: %s", err)
		}
		if vals != nil && matchFields(vals, step.Expect.Fields) {
			return true, fmt.Sprintf("%s|%s: all fields match", step.Table, step.Key), nil
		}
		return false, fmt.Sprintf("%s|%s: fields not matched", step.Table, step.Key), nil
	})
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
	expectedState := step.Expect.State

	return r.pollForDevices(ctx, step, func(name string) (bool, string, error) {
		results, err := r.Client.CheckBGPSessions(name)
		if err != nil {
			return false, "transient BGP check error", nil
		}

		for _, hc := range results {
			if hc.Status != "pass" {
				var states []string
				for _, r := range results {
					states = append(states, r.Message)
				}
				return false, "BGP not converged: " + strings.Join(states, "; "), nil
			}
		}

		// For non-default states, verify messages contain the expected state
		if expectedState != "Established" {
			for _, hc := range results {
				if !strings.Contains(hc.Message, expectedState) {
					return false, fmt.Sprintf("BGP peer not in state %s", expectedState), nil
				}
			}
		}

		return true, fmt.Sprintf("BGP %s", expectedState), nil
	})
}

// ============================================================================
// verifyHealthExecutor
// ============================================================================

type verifyHealthExecutor struct{}

func (e *verifyHealthExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.checkForDevices(step, func(name string) (StepStatus, string) {
		report, err := r.Client.HealthCheck(name)
		if err != nil {
			return StepStatusError, fmt.Sprintf("health check error: %s", err)
		}

		// Build summary
		configMsg := fmt.Sprintf("config: %d/%d entries verified",
			report.ConfigCheck.Passed, report.ConfigCheck.Passed+report.ConfigCheck.Failed)
		if report.ConfigCheck.Failed > 0 {
			// Include first few errors for diagnostics
			var errMsgs []string
			limit := 5
			if len(report.ConfigCheck.Errors) < limit {
				limit = len(report.ConfigCheck.Errors)
			}
			for _, ve := range report.ConfigCheck.Errors[:limit] {
				errMsgs = append(errMsgs, fmt.Sprintf("%s|%s.%s: expected=%q got=%q",
					ve.Table, ve.Key, ve.Field, ve.Expected, ve.Actual))
			}
			if len(report.ConfigCheck.Errors) > 5 {
				errMsgs = append(errMsgs, fmt.Sprintf("...and %d more", len(report.ConfigCheck.Errors)-5))
			}
			configMsg += " (" + strings.Join(errMsgs, "; ") + ")"
		}

		operPassed, operFailed, operWarn := 0, 0, 0
		for _, oc := range report.OperChecks {
			switch oc.Status {
			case "pass":
				operPassed++
			case "warn":
				operWarn++
			default:
				operFailed++
			}
		}
		operMsg := fmt.Sprintf("oper: %d passed", operPassed)
		if operFailed > 0 {
			operMsg += fmt.Sprintf(", %d failed", operFailed)
			// Include failed check details
			var failMsgs []string
			for _, oc := range report.OperChecks {
				if oc.Status == "fail" {
					failMsgs = append(failMsgs, oc.Message)
				}
			}
			if len(failMsgs) > 0 {
				operMsg += " (" + strings.Join(failMsgs, "; ") + ")"
			}
		}
		if operWarn > 0 {
			operMsg += fmt.Sprintf(", %d warnings", operWarn)
		}

		msg := configMsg + "; " + operMsg

		switch report.Status {
		case "pass":
			return StepStatusPassed, msg
		case "warn":
			return StepStatusPassed, msg
		default:
			return StepStatusFailed, msg
		}
	})
}

// ============================================================================
// verifyRouteExecutor
// ============================================================================

type verifyRouteExecutor struct{}

func (e *verifyRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.pollForDevices(ctx, step, func(name string) (bool, string, error) {
		var entry *newtron.RouteEntry
		var err error

		if step.Expect.Source == "asic_db" {
			entry, err = r.Client.GetRouteASIC(name, step.Prefix)
		} else {
			entry, err = r.Client.GetRoute(name, step.VRF, step.Prefix)
		}
		if err != nil {
			return false, "", err
		}

		if entry != nil && matchRoute(entry, step.Expect) {
			source := "APP_DB"
			if step.Expect.Source == "asic_db" {
				source = "ASIC_DB"
			}
			return true, fmt.Sprintf("%s via %s (%s, %s)",
				step.Prefix, step.Expect.NextHopIP, step.Expect.Protocol, source), nil
		}
		return false, fmt.Sprintf("%s not found", step.Prefix), nil
	})
}

// matchRoute returns true if the RouteEntry matches all non-empty expect fields.
func matchRoute(entry *newtron.RouteEntry, expect *ExpectBlock) bool {
	if expect.Protocol != "" && entry.Protocol != expect.Protocol {
		return false
	}
	if expect.NextHopIP != "" {
		found := false
		for _, nh := range entry.NextHops {
			if nh.Address == expect.NextHopIP {
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
			Status:  StepStatusSkipped,
			Message: fmt.Sprintf("platform %s has dataplane: false", platformName),
		}}
	}

	deviceName := r.resolveDevices(step)[0]

	// Resolve target IP
	targetIP := step.Target
	if net.ParseIP(targetIP) == nil {
		// Treat as device name, resolve to loopback IP
		info, err := r.Client.DeviceInfo(step.Target)
		if err != nil {
			return &StepOutput{Result: &StepResult{
				Status: StepStatusError,
				Message: fmt.Sprintf("target device %q: %s", step.Target, err),
			}}
		}
		targetIP = info.LoopbackIP
	}

	count := step.Count
	if count == 0 {
		count = 5
	}

	pingCmd := fmt.Sprintf("ping -c %d -W 5 %s", count, targetIP)

	var output string
	var err error
	if hostConn, ok := r.HostConns[deviceName]; ok {
		// Host device: run ping inside the network namespace via direct SSH
		cmd := fmt.Sprintf("ip netns exec %s %s", deviceName, pingCmd)
		output, err = runSSHCommand(hostConn, cmd)
	} else {
		// Switch device: run via newtron-server API
		output, err = r.Client.SSHCommand(deviceName, pingCmd)
	}
	if err != nil && output == "" {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError,
			Message: fmt.Sprintf("ping command failed: %s", err),
		}}
	}

	// Parse packet loss from output
	successRate := parsePingSuccessRate(output)
	expectedRate := 1.0
	if step.Expect != nil && step.Expect.SuccessRate != nil {
		expectedRate = *step.Expect.SuccessRate
	}

	if successRate >= expectedRate {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusPassed,
			Message: fmt.Sprintf("ping %s: %.0f%% success", targetIP, successRate*100),
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status: StepStatusFailed,
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
	opts := newtron.ApplyServiceOpts{}
	if step.Params != nil {
		if ip, ok := step.Params["ip"]; ok {
			opts.IPAddress = fmt.Sprintf("%v", ip)
		}
		opts.VLAN = intParam(step.Params, "vlan")
	}
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.ApplyService(name, step.Interface, step.Service, opts, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("apply-service %s: %s", step.Service, err)
		}
		return fmt.Sprintf("applied service %s on %s (%d changes)", step.Service, step.Interface, result.ChangeCount), nil
	})
}

// ============================================================================
// removeServiceExecutor
// ============================================================================

type removeServiceExecutor struct{}

func (e *removeServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveService(name, step.Interface, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-service: %s", err)
		}
		return fmt.Sprintf("removed service from %s (%d changes)", step.Interface, result.ChangeCount), nil
	})
}

// ============================================================================
// configureLoopbackExecutor
// ============================================================================

type configureLoopbackExecutor struct{}

func (e *configureLoopbackExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.ConfigureLoopback(name, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("configure-loopback: %s", err)
		}
		return fmt.Sprintf("configured loopback (%d changes)", result.ChangeCount), nil
	})
}

// ============================================================================
// sshCommandExecutor
// ============================================================================

type sshCommandExecutor struct{}

func (e *sshCommandExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	// When timeout is set, poll until the command succeeds (useful for boot-ssh).
	if step.Expect != nil && step.Expect.Timeout > 0 {
		return r.pollForDevices(ctx, step, func(name string) (bool, string, error) {
			output, err := r.Client.SSHCommand(name, step.Command)
			if err != nil {
				return false, fmt.Sprintf("command failed: %s", err), nil
			}
			if step.Expect != nil && step.Expect.Contains != "" {
				if strings.Contains(output, step.Expect.Contains) {
					return true, fmt.Sprintf("output contains %q", step.Expect.Contains), nil
				}
				return false, fmt.Sprintf("output does not contain %q", step.Expect.Contains), nil
			}
			return true, "command succeeded", nil
		})
	}
	return r.checkForDevices(step, func(name string) (StepStatus, string) {
		output, err := r.Client.SSHCommand(name, step.Command)
		if step.Expect != nil && step.Expect.Contains != "" {
			if strings.Contains(output, step.Expect.Contains) {
				return StepStatusPassed, fmt.Sprintf("output contains %q", step.Expect.Contains)
			}
			return StepStatusFailed, fmt.Sprintf("output does not contain %q", step.Expect.Contains)
		}
		if err == nil {
			return StepStatusPassed, "command succeeded"
		}
		return StepStatusFailed, fmt.Sprintf("command failed: %s", err)
	})
}

// ============================================================================
// restartServiceExecutor
// ============================================================================

type restartServiceExecutor struct{}

func (e *restartServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		if err := r.Client.RestartService(name, step.Service); err != nil {
			return "", fmt.Errorf("restart %s: %s", step.Service, err)
		}
		return fmt.Sprintf("restarted %s", step.Service), nil
	})
}

// ============================================================================
// configReloadExecutor
// ============================================================================

type configReloadExecutor struct{}

func (e *configReloadExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		if err := r.Client.ConfigReload(name); err != nil {
			return "", fmt.Errorf("config reload: %s", err)
		}
		return "config reloaded", nil
	})
}

// ============================================================================
// applyFRRDefaultsExecutor
// ============================================================================

type applyFRRDefaultsExecutor struct{}

func (e *applyFRRDefaultsExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		if err := r.Client.ApplyFRRDefaults(name); err != nil {
			return "", fmt.Errorf("apply FRR defaults: %s", err)
		}
		return "applied FRR defaults", nil
	})
}

// ============================================================================
// setInterfaceExecutor
// ============================================================================

type setInterfaceExecutor struct{}

func (e *setInterfaceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	property := strParam(step.Params, "property")
	value := strParam(step.Params, "value")

	return r.executeForDevices(step, func(name string) (string, error) {
		var result *newtron.WriteResult
		var err error
		switch property {
		case "ip":
			result, err = r.Client.SetIP(name, step.Interface, value, execOptsRun)
		case "vrf":
			result, err = r.Client.SetVRF(name, step.Interface, value, execOptsRun)
		default:
			result, err = r.Client.InterfaceSet(name, step.Interface, property, value, execOptsRun)
		}
		if err != nil {
			return "", fmt.Errorf("set-interface %s %s=%s: %s", step.Interface, property, value, err)
		}
		return fmt.Sprintf("set %s %s=%s (%d changes)", step.Interface, property, value, result.ChangeCount), nil
	})
}

// ============================================================================
// createVLANExecutor
// ============================================================================

type createVLANExecutor struct{}

func (e *createVLANExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.CreateVLAN(name, vlanID, "", execOptsRun)
		if err != nil {
			return "", fmt.Errorf("create-vlan %d: %s", vlanID, err)
		}
		return fmt.Sprintf("created VLAN %d (%d changes)", vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// deleteVLANExecutor
// ============================================================================

type deleteVLANExecutor struct{}

func (e *deleteVLANExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.DeleteVLAN(name, vlanID, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("delete-vlan %d: %s", vlanID, err)
		}
		return fmt.Sprintf("deleted VLAN %d (%d changes)", vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// addVLANMemberExecutor
// ============================================================================

type addVLANMemberExecutor struct{}

func (e *addVLANMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	interfaceName := step.Interface
	tagged := step.Tagging == "tagged"

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.AddVLANMember(name, vlanID, interfaceName, tagged, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("add-vlan-member %d %s: %s", vlanID, interfaceName, err)
		}
		return fmt.Sprintf("added %s to VLAN %d (%d changes)", interfaceName, vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// createVRFExecutor
// ============================================================================

type createVRFExecutor struct{}

func (e *createVRFExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.CreateVRF(name, vrfName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("create-vrf %s: %s", vrfName, err)
		}
		return fmt.Sprintf("created VRF %s (%d changes)", vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// deleteVRFExecutor
// ============================================================================

type deleteVRFExecutor struct{}

func (e *deleteVRFExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.DeleteVRF(name, vrfName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("delete-vrf %s: %s", vrfName, err)
		}
		return fmt.Sprintf("deleted VRF %s (%d changes)", vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// setupEVPNExecutor
// ============================================================================

type setupEVPNExecutor struct{}

func (e *setupEVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	sourceIP := strParam(step.Params, "source_ip")
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.SetupEVPN(name, sourceIP, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("setup-evpn (source=%s): %s", sourceIP, err)
		}
		return fmt.Sprintf("setup EVPN (source=%s, %d changes)", sourceIP, result.ChangeCount), nil
	})
}

// ============================================================================
// addVRFInterfaceExecutor
// ============================================================================

type addVRFInterfaceExecutor struct{}

func (e *addVRFInterfaceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	intfName := step.Interface
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.AddVRFInterface(name, vrfName, intfName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("add-vrf-interface %s %s: %s", vrfName, intfName, err)
		}
		return fmt.Sprintf("added %s to VRF %s (%d changes)", intfName, vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// removeVRFInterfaceExecutor
// ============================================================================

type removeVRFInterfaceExecutor struct{}

func (e *removeVRFInterfaceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	intfName := step.Interface
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveVRFInterface(name, vrfName, intfName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-vrf-interface %s %s: %s", vrfName, intfName, err)
		}
		return fmt.Sprintf("removed %s from VRF %s (%d changes)", intfName, vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// bindIPVPNExecutor
// ============================================================================

type bindIPVPNExecutor struct{}

func (e *bindIPVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	ipvpnName := strParam(step.Params, "ipvpn")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.BindIPVPN(name, vrfName, ipvpnName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("bind-ipvpn %s %s: %s", vrfName, ipvpnName, err)
		}
		return fmt.Sprintf("bound IP-VPN %s to VRF %s (%d changes)", ipvpnName, vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// unbindIPVPNExecutor
// ============================================================================

type unbindIPVPNExecutor struct{}

func (e *unbindIPVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.UnbindIPVPN(name, vrfName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("unbind-ipvpn %s: %s", vrfName, err)
		}
		return fmt.Sprintf("unbound IP-VPN from VRF %s (%d changes)", vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// bindMACVPNExecutor
// ============================================================================

type bindMACVPNExecutor struct{}

func (e *bindMACVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	macvpnName := strParam(step.Params, "macvpn")

	return r.executeForDevices(step, func(name string) (string, error) {
		vlanName := fmt.Sprintf("Vlan%d", vlanID)
		result, err := r.Client.BindMACVPN(name, vlanName, macvpnName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("bind-macvpn vlan=%d %s: %s", vlanID, macvpnName, err)
		}
		return fmt.Sprintf("bound MAC-VPN %s to VLAN %d (%d changes)", macvpnName, vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// unbindMACVPNExecutor
// ============================================================================

type unbindMACVPNExecutor struct{}

func (e *unbindMACVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	return r.executeForDevices(step, func(name string) (string, error) {
		vlanName := fmt.Sprintf("Vlan%d", vlanID)
		result, err := r.Client.UnbindMACVPN(name, vlanName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("unbind-macvpn vlan=%d: %s", vlanID, err)
		}
		return fmt.Sprintf("unbound MAC-VPN from VLAN %d (%d changes)", vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// addStaticRouteExecutor
// ============================================================================

type addStaticRouteExecutor struct{}

func (e *addStaticRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	prefix := step.Prefix
	nextHop := strParam(step.Params, "next_hop")
	metric := intParam(step.Params, "metric")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.AddStaticRoute(name, vrfName, prefix, nextHop, metric, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("add-static-route %s %s via %s: %s", vrfName, prefix, nextHop, err)
		}
		return fmt.Sprintf("added static route %s via %s in %s (%d changes)", prefix, nextHop, vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// removeStaticRouteExecutor
// ============================================================================

type removeStaticRouteExecutor struct{}

func (e *removeStaticRouteExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vrfName := step.VRF
	prefix := step.Prefix

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveStaticRoute(name, vrfName, prefix, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-static-route %s %s: %s", vrfName, prefix, err)
		}
		return fmt.Sprintf("removed static route %s from %s (%d changes)", prefix, vrfName, result.ChangeCount), nil
	})
}

// ============================================================================
// removeVLANMemberExecutor
// ============================================================================

type removeVLANMemberExecutor struct{}

func (e *removeVLANMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	interfaceName := step.Interface

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveVLANMember(name, vlanID, interfaceName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-vlan-member %d %s: %s", vlanID, interfaceName, err)
		}
		return fmt.Sprintf("removed %s from VLAN %d (%d changes)", interfaceName, vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// applyQoSExecutor
// ============================================================================

type applyQoSExecutor struct{}

func (e *applyQoSExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	intfName := step.Interface
	policyName := strParam(step.Params, "qos_policy")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.ApplyQoS(name, intfName, policyName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("apply-qos %s %s: %s", intfName, policyName, err)
		}
		return fmt.Sprintf("applied QoS policy %s on %s (%d changes)", policyName, intfName, result.ChangeCount), nil
	})
}

// ============================================================================
// removeQoSExecutor
// ============================================================================

type removeQoSExecutor struct{}

func (e *removeQoSExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	intfName := step.Interface
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveQoS(name, intfName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-qos %s: %s", intfName, err)
		}
		return fmt.Sprintf("removed QoS from %s (%d changes)", intfName, result.ChangeCount), nil
	})
}

// ============================================================================
// configureSVIExecutor
// ============================================================================

type configureSVIExecutor struct{}

func (e *configureSVIExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.ConfigureSVI(name, newtron.SVIConfigureRequest{
			VlanID:     vlanID,
			VRF:        strParam(step.Params, "vrf"),
			IPAddress:  strParam(step.Params, "ip"),
			AnycastMAC: strParam(step.Params, "anycast_mac"),
		}, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("configure-svi vlan=%d: %s", vlanID, err)
		}
		return fmt.Sprintf("configured SVI Vlan%d (%d changes)", vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// bgpAddNeighborExecutor
// ============================================================================

type bgpAddNeighborExecutor struct{}

func (e *bgpAddNeighborExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	remoteASN := intParam(step.Params, "remote_asn")
	neighborIP := strParam(step.Params, "neighbor_ip")

	return r.executeForDevices(step, func(name string) (string, error) {
		config := newtron.BGPNeighborConfig{
			NeighborIP: neighborIP,
			RemoteAS:   remoteASN,
		}
		var result *newtron.WriteResult
		var err error
		if step.Interface != "" {
			result, err = r.Client.InterfaceAddBGPNeighbor(name, step.Interface, config, execOptsRun)
		} else {
			result, err = r.Client.AddBGPNeighbor(name, config, execOptsRun)
		}
		if err != nil {
			return "", fmt.Errorf("bgp-add-neighbor %s: %s", neighborIP, err)
		}
		return fmt.Sprintf("added BGP neighbor %s ASN %d (%d changes)", neighborIP, remoteASN, result.ChangeCount), nil
	})
}

// ============================================================================
// bgpRemoveNeighborExecutor
// ============================================================================

type bgpRemoveNeighborExecutor struct{}

func (e *bgpRemoveNeighborExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	neighborIP := strParam(step.Params, "neighbor_ip")

	return r.executeForDevices(step, func(name string) (string, error) {
		var result *newtron.WriteResult
		var err error
		if step.Interface != "" {
			result, err = r.Client.InterfaceRemoveBGPNeighbor(name, step.Interface, neighborIP, execOptsRun)
		} else {
			result, err = r.Client.RemoveBGPNeighbor(name, neighborIP, execOptsRun)
		}
		if err != nil {
			return "", fmt.Errorf("bgp-remove-neighbor %s: %s", neighborIP, err)
		}
		return fmt.Sprintf("removed BGP neighbor %s (%d changes)", neighborIP, result.ChangeCount), nil
	})
}

// ============================================================================
// refreshServiceExecutor
// ============================================================================

type refreshServiceExecutor struct{}

func (e *refreshServiceExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RefreshService(name, step.Interface, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("refresh-service %s: %s", step.Interface, err)
		}
		return fmt.Sprintf("refreshed service on %s (%d changes)", step.Interface, result.ChangeCount), nil
	})
}

// ============================================================================
// cleanupExecutor
// ============================================================================

type cleanupExecutor struct{}

func (e *cleanupExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	cleanupType := strParam(step.Params, "type")
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.Cleanup(name, cleanupType, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("cleanup: %s", err)
		}
		return fmt.Sprintf("cleanup (%d changes)", result.ChangeCount), nil
	})
}

// ============================================================================
// createPortChannelExecutor
// ============================================================================

type createPortChannelExecutor struct{}

func (e *createPortChannelExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	pcName := strParam(step.Params, "name")
	members := strSliceParam(step.Params, "members")
	mtu := intParam(step.Params, "mtu")
	minLinks := intParam(step.Params, "min_links")
	fallback := boolParam(step.Params, "fallback")
	fastRate := boolParam(step.Params, "fast_rate")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.CreatePortChannel(name, newtron.PortChannelCreateRequest{
			Name:     pcName,
			Members:  members,
			MTU:      mtu,
			MinLinks: minLinks,
			Fallback: fallback,
			FastRate: fastRate,
		}, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("create-portchannel %s: %s", pcName, err)
		}
		return fmt.Sprintf("created PortChannel %s (%d changes)", pcName, result.ChangeCount), nil
	})
}

// ============================================================================
// deletePortChannelExecutor
// ============================================================================

type deletePortChannelExecutor struct{}

func (e *deletePortChannelExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	pcName := strParam(step.Params, "name")
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.DeletePortChannel(name, pcName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("delete-portchannel %s: %s", pcName, err)
		}
		return fmt.Sprintf("deleted PortChannel %s (%d changes)", pcName, result.ChangeCount), nil
	})
}

// ============================================================================
// addPortChannelMemberExecutor
// ============================================================================

type addPortChannelMemberExecutor struct{}

func (e *addPortChannelMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	pcName := strParam(step.Params, "name")
	member := strParam(step.Params, "member")
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.AddPortChannelMember(name, pcName, member, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("add-portchannel-member %s %s: %s", pcName, member, err)
		}
		return fmt.Sprintf("added %s to PortChannel %s (%d changes)", member, pcName, result.ChangeCount), nil
	})
}

// ============================================================================
// removePortChannelMemberExecutor
// ============================================================================

type removePortChannelMemberExecutor struct{}

func (e *removePortChannelMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	pcName := strParam(step.Params, "name")
	member := strParam(step.Params, "member")
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemovePortChannelMember(name, pcName, member, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-portchannel-member %s %s: %s", pcName, member, err)
		}
		return fmt.Sprintf("removed %s from PortChannel %s (%d changes)", member, pcName, result.ChangeCount), nil
	})
}

// ============================================================================
// createACLTableExecutor
// ============================================================================

type createACLTableExecutor struct{}

func (e *createACLTableExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	aclName := strParam(step.Params, "name")
	aclType := strParam(step.Params, "type")
	stage := strParam(step.Params, "stage")
	description := strParam(step.Params, "description")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.CreateACLTable(name, newtron.ACLCreateRequest{
			Name:        aclName,
			Type:        aclType,
			Stage:       stage,
			Description: description,
		}, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("create-acl-table %s: %s", aclName, err)
		}
		return fmt.Sprintf("created ACL table %s (%d changes)", aclName, result.ChangeCount), nil
	})
}

// ============================================================================
// addACLRuleExecutor
// ============================================================================

type addACLRuleExecutor struct{}

func (e *addACLRuleExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	tableName := strParam(step.Params, "name")
	ruleName := strParam(step.Params, "rule")
	action := strParam(step.Params, "action")
	priority := intParam(step.Params, "priority")
	srcIP := strParam(step.Params, "src_ip")
	dstIP := strParam(step.Params, "dst_ip")
	protocol := strParam(step.Params, "protocol")
	srcPort := strParam(step.Params, "src_port")
	dstPort := strParam(step.Params, "dst_port")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.AddACLRule(name, tableName, newtron.ACLRuleAddRequest{
			RuleName: ruleName,
			Priority: priority,
			Action:   action,
			SrcIP:    srcIP,
			DstIP:    dstIP,
			Protocol: protocol,
			SrcPort:  srcPort,
			DstPort:  dstPort,
		}, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("add-acl-rule %s|%s: %s", tableName, ruleName, err)
		}
		return fmt.Sprintf("added rule %s to ACL table %s (%d changes)", ruleName, tableName, result.ChangeCount), nil
	})
}

// ============================================================================
// deleteACLRuleExecutor
// ============================================================================

type deleteACLRuleExecutor struct{}

func (e *deleteACLRuleExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	tableName := strParam(step.Params, "name")
	ruleName := strParam(step.Params, "rule")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveACLRule(name, tableName, ruleName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("delete-acl-rule %s|%s: %s", tableName, ruleName, err)
		}
		return fmt.Sprintf("deleted rule %s from ACL table %s (%d changes)", ruleName, tableName, result.ChangeCount), nil
	})
}

// ============================================================================
// deleteACLTableExecutor
// ============================================================================

type deleteACLTableExecutor struct{}

func (e *deleteACLTableExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	aclName := strParam(step.Params, "name")

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.DeleteACLTable(name, aclName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("delete-acl-table %s: %s", aclName, err)
		}
		return fmt.Sprintf("deleted ACL table %s (%d changes)", aclName, result.ChangeCount), nil
	})
}

// ============================================================================
// bindACLExecutor
// ============================================================================

type bindACLExecutor struct{}

func (e *bindACLExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	aclName := strParam(step.Params, "name")
	direction := strParam(step.Params, "direction")
	ifaceName := step.Interface

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.BindACL(name, ifaceName, aclName, direction, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("bind-acl %s to %s (%s): %s", aclName, ifaceName, direction, err)
		}
		return fmt.Sprintf("bound ACL %s to %s (%s, %d changes)", aclName, ifaceName, direction, result.ChangeCount), nil
	})
}

// ============================================================================
// unbindACLExecutor
// ============================================================================

type unbindACLExecutor struct{}

func (e *unbindACLExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	aclName := strParam(step.Params, "name")
	ifaceName := step.Interface

	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.UnbindACL(name, ifaceName, aclName, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("unbind-acl %s from %s: %s", aclName, ifaceName, err)
		}
		return fmt.Sprintf("unbound ACL %s from %s (%d changes)", aclName, ifaceName, result.ChangeCount), nil
	})
}

// ============================================================================
// configureBGPExecutor
// ============================================================================

type configureBGPExecutor struct{}

func (e *configureBGPExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.ConfigureBGP(name, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("configure-bgp: %s", err)
		}
		return fmt.Sprintf("configured BGP (%d changes)", result.ChangeCount), nil
	})
}

// ============================================================================
// removeSVIExecutor
// ============================================================================

type removeSVIExecutor struct{}

func (e *removeSVIExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveSVI(name, vlanID, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-svi vlan=%d: %s", vlanID, err)
		}
		return fmt.Sprintf("removed SVI for VLAN %d (%d changes)", vlanID, result.ChangeCount), nil
	})
}

// ============================================================================
// removeIPExecutor
// ============================================================================

type removeIPExecutor struct{}

func (e *removeIPExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	ip := strParam(step.Params, "ip")
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveIP(name, step.Interface, ip, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-ip %s %s: %s", step.Interface, ip, err)
		}
		return fmt.Sprintf("removed IP %s from %s (%d changes)", ip, step.Interface, result.ChangeCount), nil
	})
}

// ============================================================================
// teardownEVPNExecutor
// ============================================================================

type teardownEVPNExecutor struct{}

func (e *teardownEVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.TeardownEVPN(name, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("teardown-evpn: %s", err)
		}
		return fmt.Sprintf("tore down EVPN overlay (%d changes)", result.ChangeCount), nil
	})
}

// ============================================================================
// removeBGPGlobalsExecutor
// ============================================================================

type removeBGPGlobalsExecutor struct{}

func (e *removeBGPGlobalsExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveBGPGlobals(name, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-bgp-globals: %s", err)
		}
		return fmt.Sprintf("removed BGP globals (%d changes)", result.ChangeCount), nil
	})
}

// ============================================================================
// removeLoopbackExecutor
// ============================================================================

type removeLoopbackExecutor struct{}

func (e *removeLoopbackExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(name string) (string, error) {
		result, err := r.Client.RemoveLoopback(name, execOptsRun)
		if err != nil {
			return "", fmt.Errorf("remove-loopback: %s", err)
		}
		return fmt.Sprintf("removed loopback (%d changes)", result.ChangeCount), nil
	})
}
