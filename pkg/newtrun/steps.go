package newtrun

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// stepExecutor executes a single step and returns output.
type stepExecutor interface {
	Execute(ctx context.Context, r *Runner, step *Step) *StepOutput
}

// StepOutput is the return value from every executor.
type StepOutput struct {
	Result *StepResult
}

// executors maps each StepAction to its executor implementation.
var executors = map[StepAction]stepExecutor{
	ActionProvision:          &provisionExecutor{},
	ActionWait:               &waitExecutor{},
	ActionVerifyProvisioning: &verifyProvisioningExecutor{},
	ActionHostExec:           &hostExecExecutor{},
	ActionNewtron:            &newtronExecutor{},
}

// executeForDevices runs an operation on all target devices in parallel and collects results.
// The callback fn receives the device name, returning a human-readable message and an error.
func (r *Runner) executeForDevices(step *Step, fn func(name string) (string, error)) *StepOutput {
	names := r.resolveDevices(step)
	if len(names) == 0 {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusError,
			Details: []DeviceResult{{Device: "(none)", Status: StepStatusError, Message: "no devices resolved"}},
		}}
	}
	details := make([]DeviceResult, len(names))

	var wg sync.WaitGroup
	for i, name := range names {
		// Skip host devices for SONiC-specific operations (provision, restart-service, apply-frr-defaults, etc.)
		// Host actions use the 'command' executor which doesn't call this helper.
		if _, isHost := r.HostConns[name]; isHost {
			details[i] = DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC operation not applicable)"}
			continue
		}
		wg.Add(1)
		go func(idx int, dev string) {
			defer wg.Done()
			msg, err := fn(dev)
			if err != nil {
				details[idx] = DeviceResult{Device: dev, Status: StepStatusError, Message: err.Error()}
			} else {
				details[idx] = DeviceResult{Device: dev, Status: StepStatusPassed, Message: msg}
			}
		}(i, name)
	}
	wg.Wait()

	status := StepStatusPassed
	for _, d := range details {
		if d.Status != StepStatusPassed && d.Status != StepStatusSkipped {
			status = StepStatusFailed
			break
		}
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

// checkForDevices resolves devices, calls fn for each in parallel, and collects results.
// Use for non-polling verification executors. The callback returns status and message.
func (r *Runner) checkForDevices(step *Step, fn func(name string) (StepStatus, string)) *StepOutput {
	names := r.resolveDevices(step)
	if len(names) == 0 {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusError,
			Details: []DeviceResult{{Device: "(none)", Status: StepStatusError, Message: "no devices resolved"}},
		}}
	}
	details := make([]DeviceResult, len(names))

	var wg sync.WaitGroup
	for i, name := range names {
		// Skip host devices for SONiC-specific verification (verify-health, verify-config-db, etc.)
		// Host actions use the 'host-exec' executor which doesn't call this helper.
		if _, isHost := r.HostConns[name]; isHost {
			details[i] = DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC verification not applicable)"}
			continue
		}
		wg.Add(1)
		go func(idx int, dev string) {
			defer wg.Done()
			st, msg := fn(dev)
			details[idx] = DeviceResult{Device: dev, Status: st, Message: msg}
		}(i, name)
	}
	wg.Wait()

	status := StepStatusPassed
	for _, d := range details {
		if d.Status != StepStatusPassed && d.Status != StepStatusSkipped {
			status = StepStatusFailed
			break
		}
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// pollForDevices resolves devices, polls fn for each device in parallel with timeout, and collects results.
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
	details := make([]DeviceResult, len(names))

	timeout := step.Expect.Timeout
	interval := step.Expect.PollInterval

	var wg sync.WaitGroup
	for i, name := range names {
		// Skip host devices for SONiC-specific verification (verify-bgp, verify-health, etc.)
		// Host actions use the 'host-exec' executor which doesn't call this helper.
		if _, isHost := r.HostConns[name]; isHost {
			details[i] = DeviceResult{Device: name, Status: StepStatusSkipped, Message: "host device (SONiC verification not applicable)"}
			continue
		}
		wg.Add(1)
		go func(idx int, dev string) {
			defer wg.Done()

			var matched bool
			var lastMsg string

			pollErr := pollUntil(ctx, timeout, interval, func() (bool, error) {
				done, msg, err := fn(dev)
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
				details[idx] = DeviceResult{Device: dev, Status: StepStatusError, Message: pollErr.Error()}
			} else if matched {
				details[idx] = DeviceResult{Device: dev, Status: StepStatusPassed, Message: lastMsg}
			} else {
				if lastMsg == "" {
					lastMsg = fmt.Sprintf("timeout after %s", timeout)
				}
				details[idx] = DeviceResult{Device: dev, Status: StepStatusFailed, Message: lastMsg}
			}
		}(i, name)
	}
	wg.Wait()

	status := StepStatusPassed
	for _, d := range details {
		if d.Status != StepStatusPassed && d.Status != StepStatusSkipped {
			status = StepStatusFailed
			break
		}
	}
	return &StepOutput{Result: &StepResult{Status: status, Details: details}}
}

// ============================================================================
// provisionExecutor
// ============================================================================

type provisionExecutor struct{}

func (e *provisionExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	output := r.executeForDevices(step, func(name string) (string, error) {
		// Reconcile: deliver full topology projection to the device.
		// Reconcile handles ConfigReload, wait, lock, ReplaceAll, and SaveConfig internally.
		result, err := r.Client.Reconcile(name, "topology", newtron.ExecOpts{Execute: true})
		if err != nil {
			return "", fmt.Errorf("reconcile: %s", err)
		}
		return fmt.Sprintf("provisioned (%d entries applied)", result.Applied), nil
	})

	return output
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
		// Verify by detecting drift: zero drift entries means the device
		// matches the topology projection exactly.
		entries, err := r.Client.IntentDrift(name, "topology")
		if err != nil {
			return StepStatusError, fmt.Sprintf("drift check error: %s", err)
		}
		if len(entries) == 0 {
			return StepStatusPassed, "no drift — device matches topology projection"
		}
		return StepStatusFailed, fmt.Sprintf("%d drift entries found", len(entries))
	})
}

// ============================================================================
// parsePingSuccessRate (used by hostExecExecutor in steps_host.go)
// ============================================================================

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
