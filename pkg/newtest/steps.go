package newtest

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/network"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/util"
)

// stepExecutor executes a single step and returns output.
type stepExecutor interface {
	Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
}

// StepOutput is the return value from every executor.
type StepOutput struct {
	Result     *StepResult
	ChangeSets map[string]*node.ChangeSet
}

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
	ActionApplyBaseline:      &applyBaselineExecutor{},
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
	ActionRemoveBaseline:   &removeBaselineExecutor{},
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
// The callback fn receives the device and its name, returning an optional ChangeSet,
// a human-readable message, and an error. Executors that use ExecuteOp should call
// it inside fn.
func (r *Runner) executeForDevices(step *Step, fn func(dev *node.Node, name string) (*node.ChangeSet, string, error)) *StepOutput {
	names := r.resolveDevices(step)
	if len(names) == 0 {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusError,
			Details: []DeviceResult{{Device: "(none)", Status: StepStatusError, Message: "no devices resolved"}},
		}}
	}
	details := make([]DeviceResult, 0, len(names))
	changeSets := make(map[string]*node.ChangeSet)
	allPassed := true

	for _, name := range names {
		// Skip host devices for SONiC-specific operations (provision, restart-service, apply-frr-defaults, etc.)
		// Host actions use the 'command' executor which doesn't call this helper.
		if r.Network.IsHostDevice(name) {
			details = append(details, DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC operation not applicable)"})
			continue
		}

		dev, err := r.Network.GetNode(name)
		if err != nil {
			details = append(details, DeviceResult{Device: name, Status: StepStatusError, Message: err.Error()})
			allPassed = false
			continue
		}
		cs, msg, err := fn(dev, name)
		if err != nil {
			details = append(details, DeviceResult{Device: name, Status: StepStatusError, Message: err.Error()})
			allPassed = false
			continue
		}
		if cs != nil {
			changeSets[name] = cs
			r.ChangeSets[name] = cs
		}
		details = append(details, DeviceResult{Device: name, Status: StepStatusPassed, Message: msg})
	}

	status := StepStatusPassed
	if !allPassed {
		status = StepStatusFailed
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

// checkForDevices resolves devices, calls fn for each, and collects results.
// Use for non-polling verification executors. The callback returns status and message.
func (r *Runner) checkForDevices(step *Step, fn func(dev *node.Node, name string) (StepStatus, string)) *StepOutput {
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
		if r.Network.IsHostDevice(name) {
			details = append(details, DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC verification not applicable)"})
			continue
		}

		dev, err := r.Network.GetNode(name)
		if err != nil {
			details = append(details, DeviceResult{Device: name, Status: StepStatusError, Message: err.Error()})
			allPassed = false
			continue
		}
		status, msg := fn(dev, name)
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
func (r *Runner) pollForDevices(ctx context.Context, step *Step, fn func(dev *node.Node, name string) (done bool, msg string, err error)) *StepOutput {
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
		if r.Network.IsHostDevice(name) {
			details = append(details, DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC verification not applicable)"})
			continue
		}

		dev, err := r.Network.GetNode(name)
		if err != nil {
			details = append(details, DeviceResult{Device: name, Status: StepStatusError, Message: err.Error()})
			allPassed = false
			continue
		}

		var matched bool
		var lastMsg string
		var pollErr error

		pollErr = pollUntil(ctx, timeout, interval, func() (bool, error) {
			done, msg, err := fn(dev, name)
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

	provisioner, err := network.NewTopologyProvisioner(r.Network)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError, Message: fmt.Sprintf("creating provisioner: %s", err),
		}}
	}

	var details []DeviceResult
	allPassed := true

	for _, name := range devices {
		// Skip host devices — they don't have SONiC CONFIG_DB to provision
		if r.Network.IsHostDevice(name) {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusSkipped,
				Message: "host device (no SONiC provisioning)",
			})
			continue
		}

		// Generate composite config offline, then deliver using the shared
		// connection (without disconnecting). ProvisionDevice() can't be used
		// here because it calls defer dev.Disconnect() which kills the shared
		// test runner connection.
		composite, err := provisioner.GenerateDeviceComposite(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("generate composite: %s", err),
			})
			allPassed = false
			continue
		}

		dev, err := r.Network.GetNode(name)
		if err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("get device: %s", err),
			})
			allPassed = false
			continue
		}

		if err := dev.Lock(); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("lock: %s", err),
			})
			allPassed = false
			continue
		}

		// Inject system MAC into DEVICE_METADATA before delivery.
		// Inherent: the system MAC is platform-initialized (not user config) and stored in
		// /etc/sonic/config_db.json. CompositeOverwrite replaces DEVICE_METADATA entirely;
		// the MAC must be re-injected so vlanmgrd can read it at startup.
		if mac := dev.ReadSystemMAC(); mac != "" {
			if composite.Tables != nil {
				if dm, ok := composite.Tables["DEVICE_METADATA"]; ok {
					if localhost, ok := dm["localhost"]; ok {
						localhost["mac"] = mac
					}
				}
			}
		}

		result, err := dev.DeliverComposite(composite, node.CompositeOverwrite)
		dev.Unlock()
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
		if err := dev.Refresh(); err != nil {
			details = append(details, DeviceResult{
				Device: name, Status: StepStatusFailed,
				Message: fmt.Sprintf("refresh after provision: %s", err),
			})
			allPassed = false
			continue
		}

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
	return r.checkForDevices(step, func(dev *node.Node, name string) (StepStatus, string) {
		cs, ok := r.ChangeSets[name]
		if !ok {
			return StepStatusError, "no ChangeSet accumulated (was provision run first?)"
		}
		if err := cs.Verify(dev); err != nil {
			return StepStatusError, fmt.Sprintf("verification error: %s", err)
		}
		v := cs.Verification
		total := v.Passed + v.Failed
		if v.Failed == 0 {
			return StepStatusPassed, fmt.Sprintf("%d/%d CONFIG_DB entries verified", v.Passed, total)
		}
		return StepStatusFailed, fmt.Sprintf("%d/%d CONFIG_DB entries verified (%d failed)", v.Passed, total, v.Failed)
	})
}

// ============================================================================
// verifyConfigDBExecutor
// ============================================================================

type verifyConfigDBExecutor struct{}

func (e *verifyConfigDBExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.checkForDevices(step, func(dev *node.Node, name string) (StepStatus, string) {
		if dev.ConfigDB() == nil {
			return StepStatusError, "CONFIG_DB not loaded"
		}
		result := e.checkDevice(dev, step)
		return result.status, result.message
	})
}

type checkResult struct {
	status  StepStatus
	message string
}

func (e *verifyConfigDBExecutor) checkDevice(dev *node.Node, step *Step) checkResult {
	client := dev.ConfigDBClient()
	if client == nil {
		return checkResult{StepStatusError, "no CONFIG_DB client"}
	}

	// Mode 1: min_entries
	if step.Expect.MinEntries != nil {
		var count int
		if step.Key == "" {
			// No key: count all entries in the table
			keys, err := client.TableKeys(step.Table)
			if err != nil {
				return checkResult{StepStatusError, fmt.Sprintf("scanning %s: %s", step.Table, err)}
			}
			count = len(keys)
		} else {
			// Specific key: count fields in that entry
			vals, err := client.Get(step.Table, step.Key)
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
		exists, err := client.Exists(step.Table, step.Key)
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
		vals, err := client.Get(step.Table, step.Key)
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
	return r.pollForDevices(ctx, step, func(dev *node.Node, name string) (bool, string, error) {
		stateClient := dev.StateDBClient()
		if stateClient == nil {
			return false, "", fmt.Errorf("STATE_DB client not connected")
		}
		vals, err := stateClient.GetEntry(step.Table, step.Key)
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

	return r.pollForDevices(ctx, step, func(dev *node.Node, name string) (bool, string, error) {
		results, err := dev.CheckBGPSessions(ctx)
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
	provisioner, err := network.NewTopologyProvisioner(r.Network)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError, Message: fmt.Sprintf("creating provisioner: %s", err),
		}}
	}

	return r.checkForDevices(step, func(dev *node.Node, name string) (StepStatus, string) {
		report, err := provisioner.VerifyDeviceHealth(ctx, name)
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
	return r.pollForDevices(ctx, step, func(dev *node.Node, name string) (bool, string, error) {
		var entry *sonic.RouteEntry
		var err error

		if step.Expect.Source == "asic_db" {
			entry, err = dev.GetRouteASIC(ctx, step.VRF, step.Prefix)
		} else {
			entry, err = dev.GetRoute(ctx, step.VRF, step.Prefix)
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
func matchRoute(entry *sonic.RouteEntry, expect *ExpectBlock) bool {
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
			Status:  StepStatusSkipped,
			Message: fmt.Sprintf("platform %s has dataplane: false", platformName),
		}}
	}

	deviceName := r.resolveDevices(step)[0]
	dev, err := r.Network.GetNode(deviceName)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError, Device: deviceName, Message: err.Error(),
		}}
	}

	// Resolve target IP
	targetIP := step.Target
	if net.ParseIP(targetIP) == nil {
		// Treat as device name, resolve to loopback IP
		targetDev, err := r.Network.GetNode(step.Target)
		if err != nil {
			return &StepOutput{Result: &StepResult{
				Status: StepStatusError, Device: deviceName,
				Message: fmt.Sprintf("target device %q: %s", step.Target, err),
			}}
		}
		targetIP = targetDev.LoopbackIP()
	}

	// Get SSH client from tunnel
	tunnel := dev.Tunnel()
	if tunnel == nil {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError, Device: deviceName,
			Message: "no SSH tunnel available",
		}}
	}

	sshClient := tunnel.SSHClient()
	session, err := sshClient.NewSession()
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError, Device: deviceName,
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
			Status: StepStatusError, Device: deviceName,
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
			Status: StepStatusPassed, Device: deviceName,
			Message: fmt.Sprintf("ping %s: %.0f%% success", targetIP, successRate*100),
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status: StepStatusFailed, Device: deviceName,
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
	opts := node.ApplyServiceOpts{}
	if step.Params != nil {
		if ip, ok := step.Params["ip"]; ok {
			opts.IPAddress = fmt.Sprintf("%v", ip)
		}
	}
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			iface, err := dev.GetInterface(step.Interface)
			if err != nil {
				return nil, fmt.Errorf("getting interface %s: %s", step.Interface, err)
			}
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			iface, err := dev.GetInterface(step.Interface)
			if err != nil {
				return nil, fmt.Errorf("getting interface %s: %s", step.Interface, err)
			}
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	return r.checkForDevices(step, func(dev *node.Node, name string) (StepStatus, string) {
		tunnel := dev.Tunnel()
		if tunnel == nil {
			return StepStatusError, "no SSH tunnel available"
		}
		output, err := tunnel.ExecCommand(step.Command)
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		if err := dev.RestartService(ctx, step.Service); err != nil {
			return nil, "", fmt.Errorf("restart %s: %s", step.Service, err)
		}
		return nil, fmt.Sprintf("restarted %s", step.Service), nil
	})
}

// ============================================================================
// configReloadExecutor
// ============================================================================

type configReloadExecutor struct{}

func (e *configReloadExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		if err := dev.ConfigReload(ctx); err != nil {
			return nil, "", fmt.Errorf("config reload: %s", err)
		}
		return nil, "config reloaded", nil
	})
}

// ============================================================================
// applyFRRDefaultsExecutor
// ============================================================================

type applyFRRDefaultsExecutor struct{}

func (e *applyFRRDefaultsExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			iface, err := dev.GetInterface(step.Interface)
			if err != nil {
				return nil, fmt.Errorf("getting interface %s: %s", step.Interface, err)
			}
			switch property {
			case "ip":
				return iface.SetIP(ctx, value)
			case "vrf":
				return iface.SetVRF(ctx, value)
			default:
				return iface.Set(ctx, property, value)
			}
		})
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
	vlanID := step.VLANID
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.CreateVLAN(ctx, vlanID, node.VLANConfig{})
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
	vlanID := step.VLANID
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	vlanID := step.VLANID
	interfaceName := step.Interface
	tagged := step.Tagging == "tagged"

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.CreateVRF(ctx, vrfName, node.VRFConfig{})
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
			Status: StepStatusError, Message: fmt.Sprintf("IP-VPN lookup: %s", err),
		}}
	}

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	vlanID := step.VLANID
	macvpnName := strParam(step.Params, "macvpn")

	macvpnDef, err := r.Network.GetMACVPN(macvpnName)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError, Message: fmt.Sprintf("MAC-VPN lookup: %s", err),
		}}
	}

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		vlanName := fmt.Sprintf("Vlan%d", vlanID)
		intf, err := dev.GetInterface(vlanName)
		if err != nil {
			return nil, "", fmt.Errorf("bind-macvpn: %s", err)
		}
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return intf.BindMACVPN(ctx, macvpnName, macvpnDef)
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
	vlanID := step.VLANID
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		vlanName := fmt.Sprintf("Vlan%d", vlanID)
		intf, err := dev.GetInterface(vlanName)
		if err != nil {
			return nil, "", fmt.Errorf("unbind-macvpn: %s", err)
		}
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return intf.UnbindMACVPN(ctx)
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	vlanID := step.VLANID
	interfaceName := step.Interface

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
			Status: StepStatusError, Message: fmt.Sprintf("QoS policy lookup: %s", err),
		}}
	}

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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
	vlanID := step.VLANID
	opts := node.SVIConfig{
		VRF:       strParam(step.Params, "vrf"),
		IPAddress: strParam(step.Params, "ip"),
	}
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			if step.Interface != "" {
				iface, err := dev.GetInterface(step.Interface)
				if err != nil {
					return nil, fmt.Errorf("getting interface %s: %s", step.Interface, err)
				}
				return iface.AddBGPNeighbor(ctx, node.DirectBGPNeighborConfig{
					NeighborIP: neighborIP,
					RemoteAS:   remoteASN,
				})
			}
			return dev.AddLoopbackBGPNeighbor(ctx, neighborIP, remoteASN, "", false)
		})
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			if step.Interface != "" {
				iface, err := dev.GetInterface(step.Interface)
				if err != nil {
					return nil, fmt.Errorf("getting interface %s: %s", step.Interface, err)
				}
				return iface.RemoveBGPNeighbor(ctx, neighborIP)
			}
			return dev.RemoveBGPNeighbor(ctx, neighborIP)
		})
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			iface, err := dev.GetInterface(step.Interface)
			if err != nil {
				return nil, fmt.Errorf("getting interface %s: %s", step.Interface, err)
			}
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
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		var summary *node.CleanupSummary
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.CreatePortChannel(ctx, pcName, node.PortChannelConfig{
				Members:  members,
				MTU:      mtu,
				MinLinks: minLinks,
				Fallback: fallback,
				FastRate: fastRate,
			})
		})
		if err != nil {
			return nil, "", fmt.Errorf("create-portchannel %s: %s", pcName, err)
		}
		return cs, fmt.Sprintf("created PortChannel %s (%d changes)", pcName, len(cs.Changes)), nil
	})
}

// ============================================================================
// deletePortChannelExecutor
// ============================================================================

type deletePortChannelExecutor struct{}

func (e *deletePortChannelExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	pcName := strParam(step.Params, "name")
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.DeletePortChannel(ctx, pcName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("delete-portchannel %s: %s", pcName, err)
		}
		return cs, fmt.Sprintf("deleted PortChannel %s (%d changes)", pcName, len(cs.Changes)), nil
	})
}

// ============================================================================
// addPortChannelMemberExecutor
// ============================================================================

type addPortChannelMemberExecutor struct{}

func (e *addPortChannelMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	pcName := strParam(step.Params, "name")
	member := strParam(step.Params, "member")
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.AddPortChannelMember(ctx, pcName, member)
		})
		if err != nil {
			return nil, "", fmt.Errorf("add-portchannel-member %s %s: %s", pcName, member, err)
		}
		return cs, fmt.Sprintf("added %s to PortChannel %s (%d changes)", member, pcName, len(cs.Changes)), nil
	})
}

// ============================================================================
// removePortChannelMemberExecutor
// ============================================================================

type removePortChannelMemberExecutor struct{}

func (e *removePortChannelMemberExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	pcName := strParam(step.Params, "name")
	member := strParam(step.Params, "member")
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.RemovePortChannelMember(ctx, pcName, member)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-portchannel-member %s %s: %s", pcName, member, err)
		}
		return cs, fmt.Sprintf("removed %s from PortChannel %s (%d changes)", member, pcName, len(cs.Changes)), nil
	})
}

// ============================================================================
// createACLTableExecutor
// ============================================================================

type createACLTableExecutor struct{}

func (e *createACLTableExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	name := strParam(step.Params, "name")
	aclType := strParam(step.Params, "type")
	stage := strParam(step.Params, "stage")
	description := strParam(step.Params, "description")

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.CreateACLTable(ctx, name, node.ACLTableConfig{
				Type:        aclType,
				Stage:       stage,
				Description: description,
			})
		})
		if err != nil {
			return nil, "", fmt.Errorf("create-acl-table %s: %s", name, err)
		}
		return cs, fmt.Sprintf("created ACL table %s (%d changes)", name, len(cs.Changes)), nil
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.AddACLRule(ctx, tableName, ruleName, node.ACLRuleConfig{
				Priority: priority,
				Action:   action,
				SrcIP:    srcIP,
				DstIP:    dstIP,
				Protocol: protocol,
				SrcPort:  srcPort,
				DstPort:  dstPort,
			})
		})
		if err != nil {
			return nil, "", fmt.Errorf("add-acl-rule %s|%s: %s", tableName, ruleName, err)
		}
		return cs, fmt.Sprintf("added rule %s to ACL table %s (%d changes)", ruleName, tableName, len(cs.Changes)), nil
	})
}

// ============================================================================
// deleteACLRuleExecutor
// ============================================================================

type deleteACLRuleExecutor struct{}

func (e *deleteACLRuleExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	tableName := strParam(step.Params, "name")
	ruleName := strParam(step.Params, "rule")

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.DeleteACLRule(ctx, tableName, ruleName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("delete-acl-rule %s|%s: %s", tableName, ruleName, err)
		}
		return cs, fmt.Sprintf("deleted rule %s from ACL table %s (%d changes)", ruleName, tableName, len(cs.Changes)), nil
	})
}

// ============================================================================
// deleteACLTableExecutor
// ============================================================================

type deleteACLTableExecutor struct{}

func (e *deleteACLTableExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	name := strParam(step.Params, "name")

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.DeleteACLTable(ctx, name)
		})
		if err != nil {
			return nil, "", fmt.Errorf("delete-acl-table %s: %s", name, err)
		}
		return cs, fmt.Sprintf("deleted ACL table %s (%d changes)", name, len(cs.Changes)), nil
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

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		intf, err := dev.GetInterface(ifaceName)
		if err != nil {
			return nil, "", fmt.Errorf("bind-acl: %s", err)
		}
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return intf.BindACL(ctx, aclName, direction)
		})
		if err != nil {
			return nil, "", fmt.Errorf("bind-acl %s to %s (%s): %s", aclName, ifaceName, direction, err)
		}
		return cs, fmt.Sprintf("bound ACL %s to %s (%s, %d changes)", aclName, ifaceName, direction, len(cs.Changes)), nil
	})
}

// ============================================================================
// unbindACLExecutor
// ============================================================================

type unbindACLExecutor struct{}

func (e *unbindACLExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	aclName := strParam(step.Params, "name")
	ifaceName := step.Interface

	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.UnbindACLFromInterface(ctx, aclName, ifaceName)
		})
		if err != nil {
			return nil, "", fmt.Errorf("unbind-acl %s from %s: %s", aclName, ifaceName, err)
		}
		return cs, fmt.Sprintf("unbound ACL %s from %s (%d changes)", aclName, ifaceName, len(cs.Changes)), nil
	})
}

// ============================================================================
// configureBGPExecutor
// ============================================================================

type configureBGPExecutor struct{}

func (e *configureBGPExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.ConfigureBGP(ctx)
		})
		if err != nil {
			return nil, "", fmt.Errorf("configure-bgp: %s", err)
		}
		return cs, fmt.Sprintf("configured BGP (%d changes)", len(cs.Changes)), nil
	})
}

// ============================================================================
// removeSVIExecutor
// ============================================================================

type removeSVIExecutor struct{}

func (e *removeSVIExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	vlanID := step.VLANID
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.RemoveSVI(ctx, vlanID)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-svi vlan=%d: %s", vlanID, err)
		}
		return cs, fmt.Sprintf("removed SVI for VLAN %d (%d changes)", vlanID, len(cs.Changes)), nil
	})
}

// ============================================================================
// removeIPExecutor
// ============================================================================

type removeIPExecutor struct{}

func (e *removeIPExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	ip := strParam(step.Params, "ip")
	return r.executeForDevices(step, func(dev *node.Node, devName string) (*node.ChangeSet, string, error) {
		intf, err := dev.GetInterface(step.Interface)
		if err != nil {
			return nil, "", fmt.Errorf("remove-ip: %s", err)
		}
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return intf.RemoveIP(ctx, ip)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-ip %s %s: %s", step.Interface, ip, err)
		}
		return cs, fmt.Sprintf("removed IP %s from %s (%d changes)", ip, step.Interface, len(cs.Changes)), nil
	})
}

// ============================================================================
// teardownEVPNExecutor
// ============================================================================

type teardownEVPNExecutor struct{}

func (e *teardownEVPNExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.TeardownEVPN(ctx)
		})
		if err != nil {
			return nil, "", fmt.Errorf("teardown-evpn: %s", err)
		}
		return cs, fmt.Sprintf("tore down EVPN overlay (%d changes)", len(cs.Changes)), nil
	})
}

// ============================================================================
// removeBGPGlobalsExecutor
// ============================================================================

type removeBGPGlobalsExecutor struct{}

func (e *removeBGPGlobalsExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.RemoveBGPGlobals(ctx)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-bgp-globals: %s", err)
		}
		return cs, fmt.Sprintf("removed BGP globals (%d changes)", len(cs.Changes)), nil
	})
}

// ============================================================================
// removeBaselineExecutor
// ============================================================================

type removeBaselineExecutor struct{}

func (e *removeBaselineExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	return r.executeForDevices(step, func(dev *node.Node, _ string) (*node.ChangeSet, string, error) {
		cs, err := dev.ExecuteOp(func() (*node.ChangeSet, error) {
			return dev.RemoveBaseline(ctx)
		})
		if err != nil {
			return nil, "", fmt.Errorf("remove-baseline: %s", err)
		}
		return cs, fmt.Sprintf("removed baseline (%d changes)", len(cs.Changes)), nil
	})
}

